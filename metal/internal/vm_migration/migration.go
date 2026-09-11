package vmmigration

import (
	"context"
	"errors"
	"fmt"
	"github.com/frappe/atlas/metal/internal/vm"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"
)

// MigrationSourceClient calls the source host during migration. Every call
// carries the VM ID so the source can resolve the migration over the mesh.
type MigrationSourceClient interface {
	// PrepareSource locks the source and returns portable state.
	PrepareSource(ctx context.Context, address, migrationID, virtualMachineID string) (PortableConfig, vm.State, error)
	// NextSnapshot acknowledges a sequence and asks for the next snapshot.
	NextSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error)
	// StreamSnapshot reads one snapshot into w and returns its byte count.
	// A positive throughputMiBps limits source disk throughput.
	StreamSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error)
	// StopSource stops the source, removes its network, and returns its final snapshot.
	StopSource(ctx context.Context, address, migrationID, virtualMachineID string) (SourceSnapshot, error)
	// StartSource restores the source during rollback.
	StartSource(ctx context.Context, address, migrationID, virtualMachineID string) error
	// FinishSource destroys the stopped source and migration state.
	FinishSource(ctx context.Context, address, migrationID, virtualMachineID string) error
	// RemoveSource unlocks the source and removes migration state.
	RemoveSource(ctx context.Context, address, migrationID, virtualMachineID string) error
}

// DiskTransfer runs ZFS operations for one migration.
type DiskTransfer interface {
	CreateSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error
	RemoveSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error
	SnapshotGUID(ctx context.Context, virtualMachineID, snapshotName string) (string, error)
	EstimateStreamBytes(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName string) (int64, error)
	SendSnapshot(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName, resumeToken string, w io.Writer) (int64, error)
	TargetDatasetExists(ctx context.Context, virtualMachineID string) (bool, error)
	ReceiveResumeToken(ctx context.Context, virtualMachineID string) (string, error)
	ReceiveSnapshot(ctx context.Context, virtualMachineID string, r io.Reader) error
	AbortReceive(ctx context.Context, virtualMachineID string) error
}

// Machines is the VM manager behavior that migration drives. The vm.Manager
// satisfies it, so the daemon passes the concrete manager while this package
// stays testable with a fake.
type Machines interface {
	ReadDesired(virtualMachineID string) (vm.DesiredRecord, error)
	ReadObserved(virtualMachineID string) (vm.ObservedRecord, error)
	WriteDesired(record vm.DesiredRecord) error
	WriteObserved(virtualMachineID string, record vm.ObservedRecord) error
	RemoveRecords(virtualMachineID string) error
	DesiredPath(virtualMachineID string) string
	ObservedPath(virtualMachineID string) string
	AllocateUserID() (uint32, error)
	LockAllocation() func()
	LockOperation(ctx context.Context, virtualMachineID string) (func(), error)
	ReleaseStorage(ctx context.Context, virtualMachineID string) error
	MachinesDirectory() string
	NormalizeSourceToStopped(ctx context.Context, virtualMachineID string) error
	RemoveMigrationNetwork(ctx context.Context, virtualMachineID string) error
	EnsureMigrationNetwork(ctx context.Context, virtualMachineID string) error
	ApplyMigratedTargetState(ctx context.Context, virtualMachineID string) error
	RestoreRuntimeState(ctx context.Context, virtualMachineID string, desiredState vm.State) error
	RemoveMigratedRuntime(ctx context.Context, virtualMachineID string) error
	RefreshSourceDisk(ctx context.Context, virtualMachineID string) error
	LimitSourceDisk(ctx context.Context, virtualMachineID string, throughputMiBps int) (int, error)
}

// reservationTimeout releases a target reservation after a lost handshake.
const reservationTimeout = 10 * time.Minute

// defaultFinalDeltaMiB is the default cutover threshold.
const defaultFinalDeltaMiB = 512

// MigrationSettings holds host migration limits.
type MigrationSettings struct {
	// FinalDeltaMiB is the cutover threshold for the final snapshot.
	FinalDeltaMiB int
}

// TargetReservation is capacity held by one migration target.
type TargetReservation struct {
	VirtualMachineID string
	VirtualCPUCount  int
	MemoryMiB        int
	DiskMiB          int
}

// AvailableCapacity is free host capacity checked by a target reservation.
type AvailableCapacity struct {
	CPUCount   int
	MemoryMiB  int
	StorageMiB int
}

// CapacitySource reports free target-host capacity.
type CapacitySource func(ctx context.Context) (AvailableCapacity, error)

// VMMigration owns host migration records and reservations.
type VMMigration struct {
	machines Machines
	store    *migrationStore
	source   MigrationSourceClient
	transfer DiskTransfer
	capacity CapacitySource
	settings MigrationSettings
	locks    vm.KeyedLocks
	logger   *slog.Logger
	now      func() time.Time

	// The manager owns one cancellable worker per VM.
	transfersMutex     sync.Mutex
	transfers          map[string]*transferHandle
	transfersWaitGroup sync.WaitGroup
	rootContext        context.Context
	rootCancel         context.CancelFunc
	closed             bool

	// The source host tracks its in-flight disk streams so an unlock can stop them.
	sourceStreamsMutex sync.Mutex
	sourceStreams      map[string]*transferHandle
}

// NewVMMigration validates records and returns a host migration manager.
func NewVMMigration(machines Machines, source MigrationSourceClient, transfer DiskTransfer, capacity CapacitySource, settings MigrationSettings, logger *slog.Logger) (*VMMigration, error) {
	if machines == nil || source == nil || transfer == nil || capacity == nil {
		return nil, fmt.Errorf("migration manager dependencies are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if settings.FinalDeltaMiB <= 0 {
		settings.FinalDeltaMiB = defaultFinalDeltaMiB
	}
	store := newMigrationStore(machines.MachinesDirectory())
	if err := store.dropUnreadableRecords(logger); err != nil {
		return nil, fmt.Errorf("clean migration records: %w", err)
	}
	rootContext, rootCancel := context.WithCancel(context.Background())
	return &VMMigration{
		machines:      machines,
		store:         store,
		source:        source,
		transfer:      transfer,
		capacity:      capacity,
		settings:      settings,
		logger:        logger,
		now:           func() time.Time { return time.Now().UTC() },
		transfers:     make(map[string]*transferHandle),
		sourceStreams: make(map[string]*transferHandle),
		rootContext:   rootContext,
		rootCancel:    rootCancel,
	}, nil
}

// CreateTarget reserves a VM ID or returns a matching in-progress record.
func (m *VMMigration) CreateTarget(ctx context.Context, migrationID, virtualMachineID, source string) (TargetMigrationRecord, error) {
	if !vm.ValidIdentifier(migrationID) || !vm.ValidIdentifier(virtualMachineID) || source == "" {
		return TargetMigrationRecord{}, vm.ErrConflict
	}
	unlock, err := m.locks.Lock(ctx, virtualMachineID)
	if err != nil {
		return TargetMigrationRecord{}, err
	}
	defer unlock()

	if otherVirtualMachineID, _, err := m.store.findTarget(migrationID); err == nil {
		if otherVirtualMachineID != virtualMachineID {
			return TargetMigrationRecord{}, vm.ErrConflict
		}
	} else if !errors.Is(err, vm.ErrNotFound) {
		return TargetMigrationRecord{}, err
	}

	existing, err := m.store.readTarget(virtualMachineID)
	if err == nil {
		if existing.ID == migrationID {
			// Return an active retry or a settled result unchanged.
			if isTerminalStatus(existing.Status) {
				return existing, nil
			}
			if existing.Source != source {
				return TargetMigrationRecord{}, vm.ErrConflict
			}
			return existing, nil
		}
		// Replace only a clean aborted remnant. Active and completed records conflict.
		if existing.Status != MigrationAborted {
			return TargetMigrationRecord{}, vm.ErrConflict
		}
		if err := m.assertReplaceableAborted(ctx, virtualMachineID); err != nil {
			return TargetMigrationRecord{}, err
		}
		if err := m.store.remove(virtualMachineID); err != nil {
			return TargetMigrationRecord{}, err
		}
	} else if !errors.Is(err, vm.ErrNotFound) {
		return TargetMigrationRecord{}, err
	}

	unlockAllocation := m.machines.LockAllocation()
	defer unlockAllocation()
	if err := m.assertVirtualMachineIDFree(virtualMachineID); err != nil {
		return TargetMigrationRecord{}, err
	}
	record := TargetMigrationRecord{
		ID:               migrationID,
		VirtualMachineID: virtualMachineID,
		Source:           source,
		Status:           MigrationRunning,
		Phase:            PhasePreparing,
		CreatedAt:        m.now(),
	}
	if err := m.store.writeTarget(record); err != nil {
		return TargetMigrationRecord{}, errors.Join(err, m.store.remove(virtualMachineID))
	}
	return record, nil
}

// TargetStatus returns a target record by migration ID.
func (m *VMMigration) TargetStatus(_ context.Context, migrationID string) (TargetMigrationRecord, error) {
	_, record, err := m.store.findTarget(migrationID)
	return record, err
}

// AbortTarget records an abort and cancels active transfer. The worker rolls back.
func (m *VMMigration) AbortTarget(ctx context.Context, migrationID string) error {
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

// requestAbort records abort intent under the migration lock.
func (m *VMMigration) requestAbort(ctx context.Context, virtualMachineID string) error {
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
	if record.Status == MigrationAborted {
		return nil
	}
	if record.FinishRequested || record.Status == MigrationCompleted {
		return vm.ErrConflict
	}
	if record.AbortRequested {
		return nil
	}
	record.AbortRequested = true
	return m.store.writeTarget(record)
}

// TargetReservations returns capacity held by active targets.
// A target reserves compute only after the handshake supplies its config.
func (m *VMMigration) TargetReservations(_ context.Context) ([]TargetReservation, error) {
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
		if record.Config == nil || (record.Status != MigrationRunning && record.Status != MigrationReady) {
			continue
		}
		specification := record.Config.Specification
		reservations = append(reservations, TargetReservation{
			VirtualMachineID: virtualMachineID,
			VirtualCPUCount:  specification.VirtualCPUCount,
			MemoryMiB:        specification.MemoryMiB,
			DiskMiB:          specification.DiskMiB,
		})
	}
	return reservations, nil
}

// assertReplaceableAborted confirms an aborted remnant left no VM, source lock,
// or target dataset.
func (m *VMMigration) assertReplaceableAborted(ctx context.Context, virtualMachineID string) error {
	if _, err := m.machines.ReadDesired(virtualMachineID); err == nil {
		return vm.ErrConflict
	} else if !errors.Is(err, vm.ErrNotFound) {
		return err
	}
	if m.IsSourceLocked(virtualMachineID) {
		return vm.ErrConflict
	}
	exists, err := m.transfer.TargetDatasetExists(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	if exists {
		return vm.ErrConflict
	}
	return nil
}

// assertVirtualMachineIDFree rejects IDs held by a VM or migration.
func (m *VMMigration) assertVirtualMachineIDFree(virtualMachineID string) error {
	if _, err := m.machines.ReadDesired(virtualMachineID); err == nil {
		return vm.ErrConflict
	} else if !errors.Is(err, vm.ErrNotFound) {
		return err
	}
	if m.IsSourceLocked(virtualMachineID) {
		return vm.ErrConflict
	}
	return nil
}

// removeTargetStaging removes reconstructed placeholders but keeps migration records.
func (m *VMMigration) removeTargetStaging(virtualMachineID string) error {
	for _, path := range []string{m.machines.DesiredPath(virtualMachineID), m.machines.ObservedPath(virtualMachineID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove target staging: %w", err)
		}
	}
	return nil
}
