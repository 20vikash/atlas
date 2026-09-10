package vm

import (
	"context"
	"errors"
)

// RequestFinish records the finish intent for a ready migration and is valid
// only after the target reports ready. A repeat is safe. A pending abort wins,
// so a finish after an abort request conflicts.
func (m *MigrationManager) RequestFinish(ctx context.Context, migrationID string) error {
	virtualMachineID, _, err := m.store.findTarget(migrationID)
	if err != nil {
		return err
	}
	unlock, err := m.locks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.readTarget(virtualMachineID)
	if err != nil {
		return err
	}
	if record.Status == MigrationCompleted {
		return nil
	}
	if record.AbortRequested || record.Status != MigrationReady {
		return ErrConflict
	}
	if record.FinishRequested {
		return nil
	}
	record.FinishRequested = true
	return m.store.writeTarget(record)
}

// advanceFinish commits a ready migration: it destroys the source, then writes
// a compact completed record and removes the token. Each step is idempotent, so
// a restart repeats safely.
func (m *MigrationManager) advanceFinish(ctx context.Context, record TargetMigrationRecord) error {
	if record.Phase != PhaseFinishing {
		record.Phase = PhaseFinishing
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	token, err := m.store.readToken(record.VirtualMachineID)
	if err == nil {
		if err := m.source.FinishSource(ctx, record.Source, record.ID, token); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	if err := m.store.writeTarget(terminalTargetRecord(record, MigrationCompleted, m.now())); err != nil {
		return err
	}
	return m.store.removeToken(record.VirtualMachineID)
}

// advanceAbort rolls a migration back to aborted. It cleans the target, then
// either unlocks a still-available source or restores a stopped source before it
// unlocks. Each step is checkpointed. A step error keeps both hosts locked and
// leaves the rollback to resume on the next pass.
func (m *MigrationManager) advanceAbort(ctx context.Context, record TargetMigrationRecord) error {
	if record.Phase != PhaseRollback {
		record.Phase = PhaseRollback
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	record, err := m.cleanAbortTarget(ctx, record)
	if err != nil {
		return err
	}

	token, err := m.store.readToken(record.VirtualMachineID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	// A stopped source is restored before it is unlocked.
	if record.SourceStopped && !record.SourceRestored {
		if err := m.source.StartSource(ctx, record.Source, record.ID, token); err != nil {
			return err
		}
		record.SourceRestored = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}
	if !record.SourceUnlocked {
		if err := m.source.RemoveSource(ctx, record.Source, record.ID, token); err != nil {
			return err
		}
		record.SourceUnlocked = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	if err := m.store.writeTarget(terminalTargetRecord(record, MigrationAborted, m.now())); err != nil {
		return err
	}
	return m.store.removeToken(record.VirtualMachineID)
}

// cleanAbortTarget removes the target runtime, network, dataset, and staging
// records. It runs for both abort paths and skips a completed step. A target
// that never reconstructed its records has nothing to remove.
func (m *MigrationManager) cleanAbortTarget(ctx context.Context, record TargetMigrationRecord) (TargetMigrationRecord, error) {
	if !record.TargetRuntimeRemoved {
		if err := m.machines.RemoveMigratedRuntime(ctx, record.VirtualMachineID); err != nil && !errors.Is(err, ErrNotFound) {
			return record, err
		}
		record.TargetRuntimeRemoved = true
		record.TargetNetworkRemoved = true
		if err := m.store.writeTarget(record); err != nil {
			return record, err
		}
	}
	if !record.TargetStorageRemoved {
		if err := m.transfer.AbortReceive(ctx, record.VirtualMachineID); err != nil {
			return record, err
		}
		if err := m.removeTargetStaging(record.VirtualMachineID); err != nil {
			return record, err
		}
		record.TargetStorageRemoved = true
		if err := m.store.writeTarget(record); err != nil {
			return record, err
		}
	}
	return record, nil
}
