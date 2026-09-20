package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

// sourceOperations changes migration state on the source host.
type sourceOperations interface {
	// PrepareSource locks the source and returns its definition and state.
	PrepareSource(ctx context.Context, address, migrationID, virtualMachineID string) (VirtualMachineDefinition, vm.State, error)
	// NextSnapshot acknowledges a sequence and asks for the next snapshot.
	NextSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error)
	// StartSnapshotStream asks the source to start one snapshot listener.
	StartSnapshotStream(ctx context.Context, address, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int) error
	// StopSource acknowledges the last received snapshot, stops the source, and
	// returns its final snapshot.
	StopSource(ctx context.Context, address, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error)
	// StartSource restores the source during rollback.
	StartSource(ctx context.Context, address, migrationID, virtualMachineID string) error
	// FinishSource destroys the stopped source and migration state.
	FinishSource(ctx context.Context, address, migrationID, virtualMachineID string) error
	// RemoveSource unlocks the source and removes migration state.
	RemoveSource(ctx context.Context, address, migrationID, virtualMachineID string) error
}

// migrationStorage changes migration snapshots and datasets.
type migrationStorage interface {
	CreateSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error
	RemoveSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error
	SnapshotGUID(ctx context.Context, virtualMachineID, snapshotName string) (string, error)
	EstimateStreamBytes(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName string) (int64, error)
	TargetDatasetExists(ctx context.Context, virtualMachineID string) (bool, error)
	ReceiveResumeToken(ctx context.Context, virtualMachineID string) (string, error)
	StartSnapshotServer(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName, resumeToken string) (storage.SourceStream, error)
	ReceiveSnapshotTLS(ctx context.Context, virtualMachineID, sourceAddress string) error
	AbortReceive(ctx context.Context, virtualMachineID string) error
}

// migrationHost changes VM state during migration.
type migrationHost interface {
	ReadDesired(virtualMachineID string) (vm.DesiredRecord, error)
	ReadObserved(virtualMachineID string) (vm.ObservedRecord, error)
	WriteDesired(record vm.DesiredRecord) error
	WriteObserved(virtualMachineID string, record vm.ObservedRecord) error
	RemoveVirtualMachineRecords(virtualMachineID string) error
	DesiredPath(virtualMachineID string) string
	ObservedPath(virtualMachineID string) string
	AllocateUserID() (uint32, error)
	LockUserIDAllocation() func()
	LockOperation(ctx context.Context, virtualMachineID string) (func(), error)
	ReleaseStorage(ctx context.Context, virtualMachineID string) error
	VirtualMachineRecordsDirectory() string
	NormalizeSourceToStopped(ctx context.Context, virtualMachineID string) error
	RemoveMigrationNetwork(ctx context.Context, virtualMachineID string) error
	EnsureMigrationNetwork(ctx context.Context, virtualMachineID string) error
	ApplyMigratedTargetState(ctx context.Context, virtualMachineID string) error
	RestoreRuntimeState(ctx context.Context, virtualMachineID string, desiredState vm.State) error
	RemoveMigratedRuntime(ctx context.Context, virtualMachineID string) error
	RefreshSourceDisk(ctx context.Context, virtualMachineID string) error
	LimitSourceDisk(ctx context.Context, virtualMachineID string, throughputMiBps int) (int, error)
}

// reservationTimeout releases a target reservation after lost source preparation.
const reservationTimeout = 10 * time.Minute

// targetIdleTimeout rolls back a target whose controller stopped calling. It
// sits behind the Atlas visibility timeout, which is also 10 minutes, so it only
// acts when Atlas has already given the migration up.
const targetIdleTimeout = 15 * time.Minute

// targetControlWriteInterval bounds how often a poll rewrites the record.
const targetControlWriteInterval = time.Minute

// defaultFinalDeltaMiB is the default cutover threshold.
const defaultFinalDeltaMiB = 512

// TargetReservation is capacity held by one migration target.
type TargetReservation struct {
	VirtualMachineID string
	CPUMillicores    int
	MemoryMiB        int
	DiskMiB          int
}

// AvailableCapacity is free host capacity checked by a target reservation.
// CPU entitlement is oversubscribed, so it is not part of the check.
type AvailableCapacity struct {
	MemoryMiB  int
	StorageMiB int
}

// CapacitySource reports free target-host capacity.
type CapacitySource func(ctx context.Context) (AvailableCapacity, error)

// Manager owns host migration records and reservations.
type Manager struct {
	host          migrationHost
	store         *migrationStore
	source        sourceOperations
	disks         migrationStorage
	capacity      CapacitySource
	finalDeltaMiB int
	locks         vm.KeyedLocks
	logger        *slog.Logger
	now           func() time.Time

	targetCreationLocks vm.KeyedLocks

	// The manager owns one cancellable worker per VM.
	transfersMutex     sync.Mutex
	transfers          map[string]*backgroundOperation
	transfersWaitGroup sync.WaitGroup
	rootContext        context.Context
	rootCancel         context.CancelFunc
	closed             bool

	// The source host tracks its in-flight disk streams so an unlock can stop them.
	sourceStreamsMutex     sync.Mutex
	sourceStreams          map[string]*backgroundOperation
	sourceStreamsWaitGroup sync.WaitGroup
}

// NewManager validates records and returns a host migration manager.
func NewManager(host *vm.MigrationHost, source *SourceClient, disks *storage.MigrationTransfer, capacity CapacitySource, finalDeltaMiB int, logger *slog.Logger) (*Manager, error) {
	if host == nil || source == nil || disks == nil {
		return nil, fmt.Errorf("migration manager dependencies are required")
	}
	return newManager(host, source, disks, capacity, finalDeltaMiB, logger)
}

func newManager(host migrationHost, source sourceOperations, disks migrationStorage, capacity CapacitySource, finalDeltaMiB int, logger *slog.Logger) (*Manager, error) {
	if host == nil || source == nil || disks == nil || capacity == nil {
		return nil, fmt.Errorf("migration manager dependencies are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if finalDeltaMiB <= 0 {
		finalDeltaMiB = defaultFinalDeltaMiB
	}
	store := newMigrationStore(host.VirtualMachineRecordsDirectory())
	if err := store.validate(); err != nil {
		return nil, fmt.Errorf("validate migration records: %w", err)
	}
	rootContext, rootCancel := context.WithCancel(context.Background())
	return &Manager{
		host:          host,
		store:         store,
		source:        source,
		disks:         disks,
		capacity:      capacity,
		finalDeltaMiB: finalDeltaMiB,
		logger:        logger,
		now:           func() time.Time { return time.Now().UTC() },
		transfers:     make(map[string]*backgroundOperation),
		sourceStreams: make(map[string]*backgroundOperation),
		rootContext:   rootContext,
		rootCancel:    rootCancel,
	}, nil
}

// CreateTarget reserves a VM ID or returns a matching in-progress record.
func (m *Manager) CreateTarget(ctx context.Context, migrationID, virtualMachineID, source string) (TargetProgress, error) {
	if !vm.ValidIdentifier(migrationID) || !vm.ValidIdentifier(virtualMachineID) || !validSourceAddress(source) {
		return TargetProgress{}, vm.ErrConflict
	}
	unlockCreation, err := m.targetCreationLocks.Lock(ctx, migrationID)
	if err != nil {
		return TargetProgress{}, err
	}
	defer unlockCreation()

	unlock, err := m.locks.Lock(ctx, virtualMachineID)
	if err != nil {
		return TargetProgress{}, err
	}
	defer unlock()

	if otherVirtualMachineID, _, err := m.store.findTarget(migrationID); err == nil {
		if otherVirtualMachineID != virtualMachineID {
			return TargetProgress{}, vm.ErrConflict
		}
	} else if !errors.Is(err, vm.ErrNotFound) {
		return TargetProgress{}, err
	}

	existing, err := m.store.readTarget(virtualMachineID)
	if err == nil {
		if existing.ID == migrationID {
			// Return an active retry or a settled result unchanged.
			if existing.State.terminal() {
				return existing.progress(), nil
			}
			if existing.Source != source {
				return TargetProgress{}, vm.ErrConflict
			}
			return existing.progress(), nil
		}
		// Replace only a clean aborted remnant. Active and completed records conflict.
		if existing.State != targetAborted {
			return TargetProgress{}, vm.ErrConflict
		}
		if err := m.assertReplaceableAborted(ctx, virtualMachineID); err != nil {
			return TargetProgress{}, err
		}
		if err := m.store.remove(virtualMachineID); err != nil {
			return TargetProgress{}, err
		}
	} else if !errors.Is(err, vm.ErrNotFound) {
		return TargetProgress{}, err
	}

	unlockAllocation := m.host.LockUserIDAllocation()
	defer unlockAllocation()
	if err := m.assertVirtualMachineIDFree(virtualMachineID); err != nil {
		return TargetProgress{}, err
	}
	record := targetRecord{
		ID:               migrationID,
		VirtualMachineID: virtualMachineID,
		Source:           source,
		State:            targetPreparing,
		CreatedAt:        m.now(),
		LastControlAt:    m.now(),
	}
	if err := m.store.writeTarget(record); err != nil {
		return TargetProgress{}, errors.Join(err, m.store.remove(virtualMachineID))
	}
	return record.progress(), nil
}

func validSourceAddress(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

// TargetStatus returns a target record by migration ID.
func (m *Manager) TargetStatus(ctx context.Context, migrationID string) (TargetProgress, error) {
	virtualMachineID, record, err := m.store.findTarget(migrationID)
	if err != nil {
		return TargetProgress{}, err
	}
	m.noteTargetControl(ctx, virtualMachineID, record)
	return record.progress(), nil
}

// noteTargetControl records that Atlas called about this migration. It writes
// at most once a minute, because Atlas polls every few seconds and the write
// takes the migration lock the transfer worker also uses.
func (m *Manager) noteTargetControl(ctx context.Context, virtualMachineID string, record targetRecord) {
	if record.State.terminal() || m.now().Sub(record.LastControlAt) < targetControlWriteInterval {
		return
	}
	if _, err := m.mutateTarget(ctx, virtualMachineID, func(target *targetRecord) {
		target.LastControlAt = m.now()
	}); err != nil {
		m.logger.Warn("could not record the migration control time",
			"migration_id", record.ID, "virtual_machine_id", virtualMachineID, "error", err)
	}
}

// AbortTarget records an abort and cancels active transfer. The worker rolls back.
func (m *Manager) AbortTarget(ctx context.Context, migrationID string) error {
	virtualMachineID, _, err := m.store.findTarget(migrationID)
	if errors.Is(err, vm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.requestAbort(ctx, virtualMachineID); err != nil {
		return err
	}
	return m.CancelTransfer(ctx, virtualMachineID)
}

// mutateTarget applies one change to the target record under the migration lock.
// The worker, the API and the reconciler all write this record, so every change
// reads the stored copy again instead of writing one it read earlier.
func (m *Manager) mutateTarget(
	ctx context.Context,
	virtualMachineID string,
	apply func(record *targetRecord),
) (targetRecord, error) {
	unlock, err := m.locks.Lock(ctx, virtualMachineID)
	if err != nil {
		return targetRecord{}, err
	}
	defer unlock()

	record, err := m.store.readTarget(virtualMachineID)
	if err != nil {
		return targetRecord{}, err
	}
	apply(&record)
	if err := m.store.writeTarget(record); err != nil {
		return targetRecord{}, err
	}
	return record, nil
}

func (m *Manager) requestAbort(ctx context.Context, virtualMachineID string) error {
	unlock, err := m.locks.Lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.readTarget(virtualMachineID)
	if errors.Is(err, vm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.State == targetAborted {
		return nil
	}
	if record.State == targetFinishing || record.State == targetCompleted {
		return vm.ErrConflict
	}
	if record.State == targetRemovingRuntime || record.State == targetRemovingStorage ||
		record.State == targetRestoringSource || record.State == targetUnlockingSource {
		return nil
	}
	record.State = targetRemovingRuntime
	return m.store.writeTarget(record)
}

// TargetReservations returns capacity held by active targets.
// A target reserves compute only after the source supplies its VM definition.
func (m *Manager) TargetReservations(_ context.Context) ([]TargetReservation, error) {
	virtualMachineIDs, err := m.store.listVirtualMachineIDs()
	if err != nil {
		return nil, err
	}
	reservations := make([]TargetReservation, 0, len(virtualMachineIDs))
	for _, virtualMachineID := range virtualMachineIDs {
		if !m.store.has(m.store.targetPath(virtualMachineID)) {
			continue
		}
		record, err := m.store.readTarget(virtualMachineID)
		if err != nil {
			return nil, err
		}
		if record.Definition == nil || record.State.terminal() || record.State == targetFailed {
			continue
		}
		specification := record.Definition.Specification
		reservations = append(reservations, TargetReservation{
			VirtualMachineID: virtualMachineID,
			CPUMillicores:    specification.CPUMillicores,
			MemoryMiB:        specification.MemoryMiB,
			DiskMiB:          specification.DiskMiB,
		})
	}
	return reservations, nil
}

// assertReplaceableAborted confirms an aborted remnant left no VM, source lock,
// or target dataset.
func (m *Manager) assertReplaceableAborted(ctx context.Context, virtualMachineID string) error {
	if _, err := m.host.ReadDesired(virtualMachineID); err == nil {
		return vm.ErrConflict
	} else if !errors.Is(err, vm.ErrNotFound) {
		return err
	}
	if m.IsSourceLocked(virtualMachineID) {
		return vm.ErrConflict
	}
	exists, err := m.disks.TargetDatasetExists(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	if exists {
		return vm.ErrConflict
	}
	return nil
}

func (m *Manager) assertVirtualMachineIDFree(virtualMachineID string) error {
	if _, err := m.host.ReadDesired(virtualMachineID); err == nil {
		return vm.ErrConflict
	} else if !errors.Is(err, vm.ErrNotFound) {
		return err
	}
	if m.IsSourceLocked(virtualMachineID) {
		return vm.ErrConflict
	}
	return nil
}

func (m *Manager) removeTargetStaging(virtualMachineID string) error {
	for _, path := range []string{m.host.DesiredPath(virtualMachineID), m.host.ObservedPath(virtualMachineID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove target staging: %w", err)
		}
	}
	return nil
}

// Manager answers the vm.MigrationGuard questions from its own records, so
// the vm package can pause mutation and reconciliation without importing this
// package. The daemon injects it with (*vm.Manager).SetMigrationGuard.

// IsSourceLocked reports whether a migration holds the VM as a source.
func (m *Manager) IsSourceLocked(virtualMachineID string) bool {
	if !m.store.has(m.store.sourcePath(virtualMachineID)) {
		return false
	}
	record, err := m.store.readSource(virtualMachineID)
	if err != nil {
		// An unreadable record keeps the VM locked, like a target reservation.
		return true
	}
	return record.State != sourceExpired
}

// IsTargetReserved reports whether a migration reserves this VM ID. A terminal
// record releases it; an unreadable record stays reserved.
func (m *Manager) IsTargetReserved(virtualMachineID string) bool {
	if !m.store.has(m.store.targetPath(virtualMachineID)) {
		return false
	}
	record, err := m.store.readTarget(virtualMachineID)
	if err != nil {
		return true
	}
	return !record.State.terminal()
}
