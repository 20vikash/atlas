package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"
)

// MigrationSourceClient talks to the source host during a migration. The target
// host holds one, so the interface lives with its consumer.
type MigrationSourceClient interface {
	// PrepareSource locks the source VM and returns its portable config and state.
	PrepareSource(ctx context.Context, address, migrationID, virtualMachineID, token string) (PortableConfig, State, error)
	// NextSnapshot acknowledges the received sequence and asks for the next snapshot.
	NextSnapshot(ctx context.Context, address, migrationID, token string, receivedSequence int) (SourceSnapshot, error)
	// StreamSnapshot reads one snapshot stream into w and returns the byte count.
	StreamSnapshot(ctx context.Context, address, migrationID, token string, sequence int, resumeToken string, w io.Writer) (int64, error)
	// RemoveSource unlocks the source VM and removes its migration state.
	RemoveSource(ctx context.Context, address, migrationID, token string) error
}

// DiskTransfer runs the ZFS operations of one migration. The source host uses
// the snapshot and send calls; the target host uses the receive calls.
type DiskTransfer interface {
	CreateSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error
	RemoveSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error
	SnapshotGUID(ctx context.Context, virtualMachineID, snapshotName string) (string, error)
	EstimateStreamBytes(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName string) (int64, error)
	SendSnapshot(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName, resumeToken string, w io.Writer) (int64, error)
	TargetDatasetExists(ctx context.Context, virtualMachineID string) (bool, error)
	ReceiveResumeToken(ctx context.Context, virtualMachineID string) (string, error)
	ReceiveSnapshot(ctx context.Context, virtualMachineID string, r io.Reader) error
}

// reservationTimeout removes a target reservation whose worker never supplies a
// config, so a lost handshake does not hold capacity forever.
const reservationTimeout = 10 * time.Minute

// defaultFinalDeltaMiB is the incremental size at or below which the target cuts
// over when the configuration sets no value.
const defaultFinalDeltaMiB = 512

// MigrationSettings holds the tunable limits of a migration on this host.
type MigrationSettings struct {
	// FinalDeltaMiB is the incremental size at or below which the target stops
	// the source and takes the final snapshot.
	FinalDeltaMiB int
}

// TargetReservation is the host capacity that one migration target holds.
type TargetReservation struct {
	VirtualMachineID string
	VirtualCPUCount  int
	MemoryMiB        int
	DiskMiB          int
}

// AvailableCapacity is the free host capacity that a target reservation checks.
type AvailableCapacity struct {
	CPUCount   int
	MemoryMiB  int
	StorageMiB int
}

// CapacitySource reports the free host capacity at the target.
type CapacitySource func(ctx context.Context) (AvailableCapacity, error)

// MigrationManager owns the migration records and reservations on this host. The
// VM manager owns the VM records that a migration reads and reconstructs.
type MigrationManager struct {
	machines *Manager
	store    *migrationStore
	source   MigrationSourceClient
	transfer DiskTransfer
	capacity CapacitySource
	settings MigrationSettings
	locks    keyedLocks
	logger   *slog.Logger
	now      func() time.Time

	// The manager owns one background disk transfer per VM.
	transfersMutex     sync.Mutex
	transfers          map[string]struct{}
	transfersWaitGroup sync.WaitGroup
	rootContext        context.Context
	rootCancel         context.CancelFunc
	closed             bool
}

// NewMigrationManager validates the stored records and returns a migration
// manager for one host.
func NewMigrationManager(machines *Manager, source MigrationSourceClient, transfer DiskTransfer, capacity CapacitySource, settings MigrationSettings, logger *slog.Logger) (*MigrationManager, error) {
	if machines == nil || source == nil || transfer == nil || capacity == nil {
		return nil, fmt.Errorf("migration manager dependencies are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if settings.FinalDeltaMiB <= 0 {
		settings.FinalDeltaMiB = defaultFinalDeltaMiB
	}
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	if err := store.validateAll(); err != nil {
		return nil, fmt.Errorf("validate migration records: %w", err)
	}
	rootContext, rootCancel := context.WithCancel(context.Background())
	return &MigrationManager{
		machines:    machines,
		store:       store,
		source:      source,
		transfer:    transfer,
		capacity:    capacity,
		settings:    settings,
		logger:      logger,
		now:         func() time.Time { return time.Now().UTC() },
		transfers:   make(map[string]struct{}),
		rootContext: rootContext,
		rootCancel:  rootCancel,
	}, nil
}

// CreateTarget reserves the VM ID for an incoming migration, or refreshes the
// token of a matching retry. A different VM, source, or migration ID conflicts.
func (m *MigrationManager) CreateTarget(ctx context.Context, migrationID, virtualMachineID, source, token string) (TargetMigrationRecord, error) {
	if !validIdentifier(migrationID) || !validIdentifier(virtualMachineID) || source == "" || token == "" {
		return TargetMigrationRecord{}, ErrConflict
	}
	unlock, err := m.locks.lock(ctx, virtualMachineID)
	if err != nil {
		return TargetMigrationRecord{}, err
	}
	defer unlock()

	if otherVirtualMachineID, _, err := m.store.findTarget(migrationID); err == nil {
		if otherVirtualMachineID != virtualMachineID {
			return TargetMigrationRecord{}, ErrConflict
		}
	} else if !errors.Is(err, ErrNotFound) {
		return TargetMigrationRecord{}, err
	}

	existing, err := m.store.readTarget(virtualMachineID)
	if err == nil {
		if existing.ID != migrationID || existing.Source != source {
			return TargetMigrationRecord{}, ErrConflict
		}
		if err := m.store.writeToken(virtualMachineID, token); err != nil {
			return TargetMigrationRecord{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return TargetMigrationRecord{}, err
	}

	m.machines.allocationMutex.Lock()
	defer m.machines.allocationMutex.Unlock()
	if err := m.assertVirtualMachineIDFree(virtualMachineID); err != nil {
		return TargetMigrationRecord{}, err
	}
	if err := m.store.writeToken(virtualMachineID, token); err != nil {
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

// TargetStatus returns the target record for one migration ID.
func (m *MigrationManager) TargetStatus(_ context.Context, migrationID string) (TargetMigrationRecord, error) {
	_, record, err := m.store.findTarget(migrationID)
	return record, err
}

// AbortTarget removes a migration that has not finished. It unlocks the source
// and clears the reservation. A cleanup failure keeps the records for a retry.
func (m *MigrationManager) AbortTarget(ctx context.Context, migrationID string) error {
	virtualMachineID, _, err := m.store.findTarget(migrationID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	unlock, err := m.locks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.readTarget(virtualMachineID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.abortTargetLocked(ctx, record)
}

// abortTargetLocked runs the abort cleanup while the VM lock is already held.
func (m *MigrationManager) abortTargetLocked(ctx context.Context, record TargetMigrationRecord) error {
	virtualMachineID := record.VirtualMachineID
	token, err := m.store.readToken(virtualMachineID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := m.removeTargetStaging(virtualMachineID); err != nil {
		return err
	}
	if token != "" {
		if err := m.source.RemoveSource(ctx, record.Source, record.ID, token); err != nil {
			return fmt.Errorf("unlock migration source: %w", err)
		}
	}
	return m.store.remove(virtualMachineID)
}

// TargetReservations returns the capacity that active migration targets hold.
// A target reserves compute only after the handshake supplies its config.
func (m *MigrationManager) TargetReservations(_ context.Context) ([]TargetReservation, error) {
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

// assertVirtualMachineIDFree rejects a reservation when a live VM or another
// migration already holds the VM ID.
func (m *MigrationManager) assertVirtualMachineIDFree(virtualMachineID string) error {
	if _, err := m.machines.store.readDesired(virtualMachineID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if m.machines.isSourceLocked(virtualMachineID) {
		return ErrConflict
	}
	return nil
}

// removeTargetStaging removes the reconstructed placeholder records, if the
// handshake wrote them. It keeps the migration records for the abort steps.
func (m *MigrationManager) removeTargetStaging(virtualMachineID string) error {
	for _, path := range []string{m.machines.store.desiredPath(virtualMachineID), m.machines.store.observedPath(virtualMachineID)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove target staging: %w", err)
		}
	}
	return nil
}
