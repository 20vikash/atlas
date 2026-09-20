package vmmigration

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/frappe/atlas/metal/internal/vm"
)

// AdvanceTarget runs the handshake and starts or resumes transfer. It returns
// without running transfer itself.
func (m *VMMigration) AdvanceTarget(ctx context.Context, virtualMachineID string) error {
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
	// Abort takes priority over every phase.
	if record.AbortRequested && !isTerminalStatus(record.Status) {
		m.StartTransfer(virtualMachineID)
		return nil
	}
	if record.Status == MigrationReady {
		// Atlas alone releases a ready target because it may own the VM.
		if record.FinishRequested {
			m.StartTransfer(virtualMachineID)
		}
		return nil
	}
	if m.isTargetAbandoned(record) {
		m.logger.Warn("rolling back a migration target whose controller stopped calling",
			"migration_id", record.ID, "virtual_machine_id", virtualMachineID,
			"status", record.Status, "phase", record.Phase,
			"idle_seconds", int(m.now().Sub(record.LastControlAt).Seconds()))
		record.AbortRequested = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
		m.StartTransfer(virtualMachineID)
		return nil
	}
	if record.Status != MigrationRunning {
		return nil
	}
	if record.Phase == PhaseCopying || record.Phase == PhaseStopping || record.Phase == PhaseStarting {
		m.StartTransfer(virtualMachineID)
		return nil
	}
	// Expire targets that never advanced past preparing.
	if m.now().Sub(record.CreatedAt) > reservationTimeout {
		m.logger.Warn("migration target expired before the handshake started", "migration_id", record.ID)
		record.AbortRequested = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
		m.StartTransfer(virtualMachineID)
		return nil
	}

	config, observedState, err := m.source.PrepareSource(ctx, record.Source, record.ID, virtualMachineID)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	if config.VirtualMachineID != virtualMachineID {
		return m.recordTargetError(record, fmt.Errorf("source returned config for %s", config.VirtualMachineID))
	}
	if err := m.reserveAndReconstructTarget(ctx, record, config, observedState); err != nil {
		return err
	}
	m.StartTransfer(virtualMachineID)
	return nil
}

// reserveAndReconstructTarget makes the capacity check and its reservation one
// host allocation operation.
func (m *VMMigration) reserveAndReconstructTarget(
	ctx context.Context,
	record TargetMigrationRecord,
	config PortableConfig,
	observedState vm.State,
) error {
	unlockAllocation := m.machines.LockAllocation()
	defer unlockAllocation()

	if err := m.reserveShape(ctx, config.Specification); err != nil {
		return m.recordTargetError(record, err)
	}
	return m.reconstructTarget(record, config, observedState)
}

// ActiveTargetVirtualMachineIDs returns VM IDs for nonterminal target migrations.
func (m *VMMigration) ActiveTargetVirtualMachineIDs(_ context.Context) ([]string, error) {
	virtualMachineIDs, err := m.store.listVirtualMachineIDs()
	if err != nil {
		return nil, err
	}
	active := make([]string, 0, len(virtualMachineIDs))
	for _, virtualMachineID := range virtualMachineIDs {
		if !m.store.has(m.store.targetPath(virtualMachineID)) {
			continue
		}
		record, err := m.store.readTarget(virtualMachineID)
		if err != nil {
			return nil, err
		}
		if !isTerminalStatus(record.Status) {
			active = append(active, virtualMachineID)
		}
	}
	return active, nil
}

// isTargetAbandoned reports whether Atlas stopped driving this migration.
// Its caller excludes ready targets because Atlas can already own their VM.
func (m *VMMigration) isTargetAbandoned(record TargetMigrationRecord) bool {
	if isTerminalStatus(record.Status) || record.AbortRequested || record.FinishRequested {
		return false
	}
	return m.now().Sub(record.LastControlAt) > targetIdleTimeout
}

// reserveShape rejects migrations that exceed host capacity.
func (m *VMMigration) reserveShape(ctx context.Context, specification vm.Specification) error {
	available, err := m.capacity(ctx)
	if err != nil {
		return err
	}
	if specification.MemoryMiB > available.MemoryMiB ||
		specification.DiskMiB > available.StorageMiB {
		return vm.ErrConflict
	}
	return nil
}

// reconstructTarget writes local target records and enters copying.
func (m *VMMigration) reconstructTarget(record TargetMigrationRecord, config PortableConfig, observedState vm.State) error {
	userID, err := m.reuseOrAllocateUserID(config.VirtualMachineID)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	desired := vm.DesiredRecord{
		ID:                      config.VirtualMachineID,
		UserID:                  userID,
		GroupID:                 userID,
		CreateFingerprint:       config.CreateFingerprint,
		Generation:              config.Generation,
		SpecificationGeneration: config.SpecificationGeneration,
		RestartGeneration:       config.RestartGeneration,
		State:                   config.DesiredState,
		Specification:           vm.CloneSpecification(config.Specification),
	}
	observed := vm.ObservedRecord{State: vm.StateUnknown, UpdatedAt: m.now()}
	if err := m.machines.WriteDesired(desired); err != nil {
		return m.recordTargetError(record, err)
	}
	if err := m.machines.WriteObserved(config.VirtualMachineID, observed); err != nil {
		return errors.Join(m.recordTargetError(record, err), os.Remove(m.machines.DesiredPath(config.VirtualMachineID)))
	}

	record.Config = &config
	record.UserID = userID
	record.GroupID = userID
	record.Phase = PhaseCopying
	record.SourceObservedState = observedState
	record.CopyStartedAt = m.now()
	record.Error = nil
	return m.store.writeTarget(record)
}

// reuseOrAllocateUserID reuses a placeholder ID when possible.
func (m *VMMigration) reuseOrAllocateUserID(virtualMachineID string) (uint32, error) {
	if existing, err := m.machines.ReadDesired(virtualMachineID); err == nil {
		return existing.UserID, nil
	} else if !errors.Is(err, vm.ErrNotFound) {
		return 0, err
	}
	return m.machines.AllocateUserID()
}

// recordTargetError stores and returns the cause.
func (m *VMMigration) recordTargetError(record TargetMigrationRecord, cause error) error {
	record.Error = &vm.OperationError{Code: "migration_error", Message: cause.Error(), UpdatedAt: m.now()}
	if writeError := m.store.writeTarget(record); writeError != nil {
		return errors.Join(cause, writeError)
	}
	return cause
}

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
		if _, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.Phase = PhaseFinishing
		}); err != nil {
			return err
		}
	}

	if err := m.source.FinishSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
		return err
	}
	if err := m.removeReceivedSnapshots(ctx, record); err != nil {
		return err
	}

	_, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		*target = terminalTargetRecord(*target, MigrationCompleted, m.now())
	})
	return err
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
		if _, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.Phase = PhaseRollback
		}); err != nil {
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
		if record, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.SourceRestored = true
		}); err != nil {
			return err
		}
	}
	if !record.SourceUnlocked {
		if err := m.source.RemoveSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
			return err
		}
		if record, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.SourceUnlocked = true
		}); err != nil {
			return err
		}
	}

	_, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		*target = terminalTargetRecord(*target, MigrationAborted, m.now())
	})
	return err
}

// cleanAbortTarget removes target runtime, network, dataset, and staging records.
func (m *VMMigration) cleanAbortTarget(ctx context.Context, record TargetMigrationRecord) (TargetMigrationRecord, error) {
	if !record.TargetRuntimeRemoved {
		if err := m.machines.RemoveMigratedRuntime(ctx, record.VirtualMachineID); err != nil && !errors.Is(err, vm.ErrNotFound) {
			return record, err
		}
		updated, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.TargetRuntimeRemoved = true
			target.TargetNetworkRemoved = true
		})
		if err != nil {
			return record, err
		}
		record = updated
	}
	if !record.TargetStorageRemoved {
		if err := m.transfer.AbortReceive(ctx, record.VirtualMachineID); err != nil {
			return record, err
		}
		if err := m.removeTargetStaging(record.VirtualMachineID); err != nil {
			return record, err
		}
		updated, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.TargetStorageRemoved = true
		})
		if err != nil {
			return record, err
		}
		record = updated
	}
	return record, nil
}
