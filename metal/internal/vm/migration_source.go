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

// migrationSnapshotName names the source snapshot for one migration interval. It
// includes the migration ID so a stale snapshot from a prior migration on the
// same VM never collides.
func migrationSnapshotName(migrationID string, sequence int) string {
	return fmt.Sprintf("migration-%s-%d", migrationID, sequence)
}

// LockSource locks the source VM and returns its portable config and observed
// state. It rejects a missing VM and a failed or unknown VM. Another migration
// for the same VM conflicts. The same migration and caller is safe to repeat.
func (m *MigrationManager) LockSource(ctx context.Context, migrationID, virtualMachineID, caller string) (SourceHandshake, error) {
	if !validIdentifier(migrationID) || !validIdentifier(virtualMachineID) || caller == "" {
		return SourceHandshake{}, ErrConflict
	}
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	defer unlock()

	if existing, err := m.store.readSource(virtualMachineID); err == nil {
		if existing.ID != migrationID || existing.Caller != caller {
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
		Caller:           caller,
		OriginalDesired:  desired.State,
		OriginalObserved: observed.State,
		LockedAt:         m.now(),
	}
	if err := m.store.writeSource(record); err != nil {
		return SourceHandshake{}, err
	}
	return m.sourceHandshake(virtualMachineID)
}

// UnlockSource removes this migration's snapshots, restores the configured disk
// limit when needed, and unlocks the VM. It refuses while the source is stopped
// and the rollback is not complete. It does not delete the VM. It is safe to
// repeat.
func (m *MigrationManager) UnlockSource(ctx context.Context, migrationID, virtualMachineID, caller string) error {
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
	if existing.ID != migrationID || existing.Caller != caller {
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

// StartSourceRollback restores the source network and its original desired state
// during an abort after the source was stopped. It is idempotent.
func (m *MigrationManager) StartSourceRollback(ctx context.Context, migrationID, virtualMachineID, caller string) error {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID, caller)
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

// DestroySource destroys the stopped source VM and removes its migration state.
// It requires a stopped, network-removed source with a final snapshot. It is
// idempotent, and refuses to remove a VM remnant that has no source record.
func (m *MigrationManager) DestroySource(ctx context.Context, migrationID, virtualMachineID, caller string) error {
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
	if record.ID != migrationID || record.Caller != caller {
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

// assertNoSourceRemnant confirms no VM record or disk remains, so a repeated
// finish for an already-destroyed source succeeds but a remnant is refused.
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

// NextSourceSnapshot records the acknowledged sequence, rotates old snapshots,
// and returns the next snapshot for the target to pull. A repeat returns the
// same unacknowledged snapshot.
func (m *MigrationManager) NextSourceSnapshot(ctx context.Context, migrationID, virtualMachineID, caller string, receivedSequence int) (SourceSnapshot, error) {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID, caller)
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

// StopSource normalizes the source to stopped, removes its network, and creates
// the final snapshot. It returns that snapshot in the same form as the snapshot
// call. Each step is checkpointed, so a repeat resumes and returns the same
// final snapshot.
func (m *MigrationManager) StopSource(ctx context.Context, migrationID, virtualMachineID, caller string) (SourceSnapshot, error) {
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID, caller)
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
		// Replace any untransferred candidate with a snapshot taken after stop.
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

// SendSourceStream streams one requested snapshot to the target. It applies the
// temporary disk limit first, then streams without the VM lock. An unknown
// sequence is rejected.
func (m *MigrationManager) SendSourceStream(ctx context.Context, migrationID, virtualMachineID, caller string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	record, err := m.boundSourceRecord(virtualMachineID, migrationID, caller)
	if err != nil {
		return 0, err
	}
	if sequence != record.Sequence {
		return 0, ErrConflict
	}
	if err := m.applySourceDiskLimit(ctx, migrationID, virtualMachineID, caller, throughputMiBps); err != nil {
		return 0, err
	}
	name := migrationSnapshotName(migrationID, sequence)
	base := ""
	if sequence > 1 {
		base = migrationSnapshotName(migrationID, sequence-1)
	}
	return m.transfer.SendSnapshot(ctx, virtualMachineID, name, base, resumeToken, w)
}

// applySourceDiskLimit applies and saves the temporary disk limit for one
// stream. It takes the VM lock only for this quick update, then releases it
// before the stream. A retry for the same value does not write again.
func (m *MigrationManager) applySourceDiskLimit(ctx context.Context, migrationID, virtualMachineID, caller string, throughputMiBps int) error {
	if throughputMiBps <= 0 {
		return nil
	}
	unlock, err := m.machines.operationLocks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID, caller)
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

// boundSourceRecord returns the source record after it confirms the migration ID
// and caller match, so one migration cannot act on another's lock.
func (m *MigrationManager) boundSourceRecord(virtualMachineID, migrationID, caller string) (SourceMigrationRecord, error) {
	record, err := m.store.readSource(virtualMachineID)
	if err != nil {
		return SourceMigrationRecord{}, err
	}
	if record.ID != migrationID || record.Caller != caller {
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
