package vmmigration

import (
	"context"
	"errors"
	"github.com/frappe/atlas/metal/internal/vm"
)

// RequestFinish records finish intent for a ready migration. A pending abort wins.
func (m *VMMigration) RequestFinish(ctx context.Context, migrationID string) error {
	virtualMachineID, _, err := m.store.findTarget(migrationID)
	if err != nil {
		return err
	}
	unlock, err := m.locks.Lock(ctx, virtualMachineID)
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
		return vm.ErrConflict
	}
	if record.FinishRequested {
		return nil
	}
	record.FinishRequested = true
	return m.store.writeTarget(record)
}

// advanceFinish destroys the source and writes a completed record. Each step is
// idempotent, so a retry after the source is gone still completes.
func (m *VMMigration) advanceFinish(ctx context.Context, record TargetMigrationRecord) error {
	if record.Phase != PhaseFinishing {
		record.Phase = PhaseFinishing
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	if err := m.source.FinishSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
		return err
	}
	if err := m.removeReceivedSnapshots(ctx, record); err != nil {
		return err
	}

	return m.store.writeTarget(terminalTargetRecord(record, MigrationCompleted, m.now()))
}

// removeReceivedSnapshots destroys the migration snapshots left on the received
// dataset after a successful migration. The live volume keeps its data. Repeats
// are safe.
func (m *VMMigration) removeReceivedSnapshots(ctx context.Context, record TargetMigrationRecord) error {
	for _, interval := range record.Intervals {
		name := migrationSnapshotName(record.ID, interval.Sequence)
		if err := m.transfer.RemoveSnapshot(ctx, record.VirtualMachineID, name); err != nil {
			return err
		}
	}
	return nil
}

// advanceAbort cleans the target, then unlocks or restores the source. Errors
// keep both hosts locked for the next pass.
func (m *VMMigration) advanceAbort(ctx context.Context, record TargetMigrationRecord) error {
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

	// Restore a stopped source before unlocking it.
	if record.SourceStopped && !record.SourceRestored {
		if err := m.source.StartSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
			return err
		}
		record.SourceRestored = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}
	if !record.SourceUnlocked {
		if err := m.source.RemoveSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
			return err
		}
		record.SourceUnlocked = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	return m.store.writeTarget(terminalTargetRecord(record, MigrationAborted, m.now()))
}

// cleanAbortTarget removes target runtime, network, dataset, and staging records.
func (m *VMMigration) cleanAbortTarget(ctx context.Context, record TargetMigrationRecord) (TargetMigrationRecord, error) {
	if !record.TargetRuntimeRemoved {
		if err := m.machines.RemoveMigratedRuntime(ctx, record.VirtualMachineID); err != nil && !errors.Is(err, vm.ErrNotFound) {
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
