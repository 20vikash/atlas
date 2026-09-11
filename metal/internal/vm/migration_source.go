package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// SourceHandshake is the portable state that the source returns to the target.
type SourceHandshake struct {
	Config        PortableConfig
	ObservedState State
}

// SourceSnapshot is the source reply to a snapshot request.
type SourceSnapshot struct {
	Sequence  int
	SizeBytes int64
	GUID      string
}

// migrationSnapshotName includes the migration ID to avoid stale collisions.
func migrationSnapshotName(migrationID string, sequence int) string {
	return fmt.Sprintf("migration-%s-%d", migrationID, sequence)
}

// LockSource locks the source and returns portable config and observed state.
// Missing, failed, unknown, or already-migrating VMs are rejected.
func (m *MigrationManager) LockSource(ctx context.Context, migrationID, virtualMachineID string) (SourceHandshake, error) {
	if !validIdentifier(migrationID) || !validIdentifier(virtualMachineID) {
		return SourceHandshake{}, ErrConflict
	}
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	defer unlock()

	if existing, err := m.store.readSource(virtualMachineID); err == nil {
		if existing.ID != migrationID {
			return SourceHandshake{}, ErrConflict
		}
		return m.sourceHandshake(virtualMachineID)
	} else if !errors.Is(err, ErrNotFound) {
		return SourceHandshake{}, err
	}

	desired, err := m.machines.store.readDesired(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	observed, err := m.machines.store.readObserved(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	if observed.State == StateFailed || observed.State == StateUnknown {
		return SourceHandshake{}, ErrConflict
	}

	record := SourceMigrationRecord{
		ID:               migrationID,
		VirtualMachineID: virtualMachineID,
		OriginalDesired:  desired.State,
		OriginalObserved: observed.State,
		LockedAt:         m.now(),
	}
	if err := m.store.writeSource(record); err != nil {
		return SourceHandshake{}, err
	}
	return m.sourceHandshake(virtualMachineID)
}

// UnlockSource removes snapshots, restores the disk limit, and unlocks the VM.
// It refuses a stopped source before rollback completes.
func (m *MigrationManager) UnlockSource(ctx context.Context, migrationID, virtualMachineID string) error {
	if !validIdentifier(virtualMachineID) {
		return ErrConflict
	}
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	existing, err := m.store.readSource(virtualMachineID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.ID != migrationID {
		return ErrConflict
	}
	if existing.Stopped && !existing.RollbackComplete {
		return ErrConflict
	}
	if err := m.removeMigrationSnapshots(ctx, existing); err != nil {
		return err
	}
	if existing.TemporaryDiskLimitMiBps > 0 && !existing.Stopped {
		if err := m.machines.RefreshSourceDisk(ctx, virtualMachineID); err != nil {
			return err
		}
	}
	return m.store.remove(virtualMachineID)
}

// StartSourceRollback restores a stopped source during abort.
func (m *MigrationManager) StartSourceRollback(ctx context.Context, migrationID, virtualMachineID string) error {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return err
	}
	if record.RollbackComplete {
		return nil
	}
	if err := m.machines.EnsureMigrationNetwork(ctx, virtualMachineID); err != nil {
		return err
	}
	if err := m.machines.RestoreRuntimeState(ctx, virtualMachineID, record.OriginalDesired); err != nil {
		return err
	}
	record.RollbackComplete = true
	return m.store.writeSource(record)
}

// DestroySource removes a stopped source and its migration state. It requires a
// stopped, network-removed source with a final snapshot.
func (m *MigrationManager) DestroySource(ctx context.Context, migrationID, virtualMachineID string) error {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.readSource(virtualMachineID)
	if errors.Is(err, ErrNotFound) {
		return m.assertNoSourceRemnant(ctx, virtualMachineID)
	}
	if err != nil {
		return err
	}
	if record.ID != migrationID {
		return ErrConflict
	}
	if !record.Stopped || !record.NetworkRemoved || record.FinalSequence < 1 {
		return ErrConflict
	}

	if !record.DestroyRuntimeComplete {
		if err := m.machines.RemoveMigratedRuntime(ctx, virtualMachineID); err != nil {
			return err
		}
		record.DestroyRuntimeComplete = true
		if err := m.store.writeSource(record); err != nil {
			return err
		}
	}
	if !record.DestroyStorageComplete {
		if err := m.machines.storage.Release(ctx, virtualMachineID); err != nil {
			return err
		}
		record.DestroyStorageComplete = true
		if err := m.store.writeSource(record); err != nil {
			return err
		}
	}
	return m.machines.store.remove(virtualMachineID)
}

// assertNoSourceRemnant rejects leftover VM records or disks.
func (m *MigrationManager) assertNoSourceRemnant(ctx context.Context, virtualMachineID string) error {
	if _, err := m.machines.store.readDesired(virtualMachineID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	exists, err := m.transfer.TargetDatasetExists(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	if exists {
		return ErrConflict
	}
	return nil
}

// removeMigrationSnapshots removes every snapshot this migration created.
func (m *MigrationManager) removeMigrationSnapshots(ctx context.Context, record SourceMigrationRecord) error {
	highest := max(record.Sequence, record.FinalSequence)
	for sequence := 1; sequence <= highest; sequence++ {
		if err := m.transfer.RemoveSnapshot(ctx, record.VirtualMachineID, migrationSnapshotName(record.ID, sequence)); err != nil {
			return err
		}
	}
	return nil
}

// NextSourceSnapshot records the acknowledged sequence and returns the next
// snapshot. A repeat returns the same unacknowledged snapshot.
func (m *MigrationManager) NextSourceSnapshot(ctx context.Context, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error) {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return SourceSnapshot{}, err
	}

	if receivedSequence > record.AcknowledgedSequence {
		record.AcknowledgedSequence = receivedSequence
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
		if receivedSequence >= 2 {
			_ = m.transfer.RemoveSnapshot(ctx, virtualMachineID, migrationSnapshotName(migrationID, receivedSequence-1))
		}
	}

	if record.Sequence <= record.AcknowledgedSequence {
		next := record.AcknowledgedSequence + 1
		if err := m.transfer.CreateSnapshot(ctx, virtualMachineID, migrationSnapshotName(migrationID, next)); err != nil {
			return SourceSnapshot{}, err
		}
		record.Sequence = next
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	return m.describeSnapshot(ctx, virtualMachineID, migrationID, record.Sequence)
}

// StopSource stops the source, removes its network, and creates the final
// snapshot. Checkpoints make repeats safe.
func (m *MigrationManager) StopSource(ctx context.Context, migrationID, virtualMachineID string) (SourceSnapshot, error) {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return SourceSnapshot{}, err
	}

	if !record.Stopped {
		if err := m.machines.NormalizeSourceToStopped(ctx, virtualMachineID); err != nil {
			return SourceSnapshot{}, err
		}
		record.Stopped = true
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	if !record.NetworkRemoved {
		if err := m.machines.RemoveMigrationNetwork(ctx, virtualMachineID); err != nil {
			return SourceSnapshot{}, err
		}
		record.NetworkRemoved = true
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	if record.FinalSequence == 0 {
		final := record.AcknowledgedSequence + 1
		name := migrationSnapshotName(migrationID, final)
		// Replace any untransferred candidate with the post-stop snapshot.
		if err := m.transfer.RemoveSnapshot(ctx, virtualMachineID, name); err != nil {
			return SourceSnapshot{}, err
		}
		if err := m.transfer.CreateSnapshot(ctx, virtualMachineID, name); err != nil {
			return SourceSnapshot{}, err
		}
		record.Sequence = final
		record.FinalSequence = final
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	return m.describeSnapshot(ctx, virtualMachineID, migrationID, record.FinalSequence)
}

// SendSourceStream applies the disk limit and streams a requested snapshot.
func (m *MigrationManager) SendSourceStream(ctx context.Context, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return 0, err
	}
	if sequence != record.Sequence {
		return 0, ErrConflict
	}
	if err := m.applySourceDiskLimit(ctx, migrationID, virtualMachineID, throughputMiBps); err != nil {
		return 0, err
	}
	name := migrationSnapshotName(migrationID, sequence)
	base := ""
	if sequence > 1 {
		base = migrationSnapshotName(migrationID, sequence-1)
	}
	return m.transfer.SendSnapshot(ctx, virtualMachineID, name, base, resumeToken, w)
}

// applySourceDiskLimit saves a stream limit under the VM lock, then releases it.
func (m *MigrationManager) applySourceDiskLimit(ctx context.Context, migrationID, virtualMachineID string, throughputMiBps int) error {
	if throughputMiBps <= 0 {
		return nil
	}
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return err
	}
	applied, err := m.machines.LimitSourceDisk(ctx, virtualMachineID, throughputMiBps)
	if err != nil {
		return err
	}
	if applied == 0 || applied == record.TemporaryDiskLimitMiBps {
		return nil
	}
	record.TemporaryDiskLimitMiBps = applied
	return m.store.writeSource(record)
}

// describeSnapshot returns the sequence, estimated size, and GUID of one snapshot.
func (m *MigrationManager) describeSnapshot(ctx context.Context, virtualMachineID, migrationID string, sequence int) (SourceSnapshot, error) {
	name := migrationSnapshotName(migrationID, sequence)
	base := ""
	if sequence > 1 {
		base = migrationSnapshotName(migrationID, sequence-1)
	}
	sizeBytes, err := m.transfer.EstimateStreamBytes(ctx, virtualMachineID, name, base)
	if err != nil {
		return SourceSnapshot{}, err
	}
	guid, err := m.transfer.SnapshotGUID(ctx, virtualMachineID, name)
	if err != nil {
		return SourceSnapshot{}, err
	}
	return SourceSnapshot{Sequence: sequence, SizeBytes: sizeBytes, GUID: guid}, nil
}

// boundSourceRecord verifies the migration before returning the source record.
func (m *MigrationManager) boundSourceRecord(virtualMachineID, migrationID string) (SourceMigrationRecord, error) {
	record, err := m.store.readSource(virtualMachineID)
	if err != nil {
		return SourceMigrationRecord{}, err
	}
	if record.ID != migrationID {
		return SourceMigrationRecord{}, ErrConflict
	}
	return record, nil
}

// sourceHandshake reads the current VM records into the portable response.
func (m *MigrationManager) sourceHandshake(virtualMachineID string) (SourceHandshake, error) {
	desired, err := m.machines.store.readDesired(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	observed, err := m.machines.store.readObserved(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	return SourceHandshake{
		Config: PortableConfig{
			VirtualMachineID:        desired.ID,
			CreateFingerprint:       desired.CreateFingerprint,
			Generation:              desired.Generation,
			SpecificationGeneration: desired.SpecificationGeneration,
			RestartGeneration:       desired.RestartGeneration,
			DesiredState:            desired.State,
			Specification:           cloneSpecification(desired.Specification),
		},
		ObservedState: observed.State,
	}, nil
}
