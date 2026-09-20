package migration

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/frappe/atlas/metal/internal/vm"
)

// AdvanceTarget prepares the source and starts or resumes transfer. It returns
// without running transfer itself.
func (m *Manager) AdvanceTarget(ctx context.Context, virtualMachineID string) error {
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
	if record.State == targetRemovingRuntime || record.State == targetRemovingStorage ||
		record.State == targetRestoringSource || record.State == targetUnlockingSource {
		m.StartTransfer(virtualMachineID)
		return nil
	}
	if record.State == targetReady || record.State == targetFinishing {
		// Atlas alone releases a ready target because it may own the VM.
		if record.State == targetFinishing {
			m.StartTransfer(virtualMachineID)
		}
		return nil
	}
	if m.isTargetAbandoned(record) {
		m.logger.Warn("rolling back a migration target whose controller stopped calling",
			"migration_id", record.ID, "virtual_machine_id", virtualMachineID,
			"status", record.State.status(), "phase", record.State.phase(),
			"idle_seconds", int(m.now().Sub(record.LastControlAt).Seconds()))
		record.State = targetRemovingRuntime
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
		m.StartTransfer(virtualMachineID)
		return nil
	}
	if record.State == targetFailed || record.State.terminal() {
		return nil
	}
	if record.State == targetCopying || record.State == targetStopping ||
		record.State == targetCreatingNetwork || record.State == targetApplyingState {
		m.StartTransfer(virtualMachineID)
		return nil
	}
	// Expire targets that never advanced past preparing.
	if m.now().Sub(record.CreatedAt) > reservationTimeout {
		m.logger.Warn("migration target expired before source preparation started", "migration_id", record.ID)
		record.State = targetRemovingRuntime
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
		m.StartTransfer(virtualMachineID)
		return nil
	}

	definition, observedState, err := m.source.PrepareSource(ctx, record.Source, record.ID, virtualMachineID)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	if definition.VirtualMachineID != virtualMachineID {
		return m.recordTargetError(record, fmt.Errorf("source returned definition for %s", definition.VirtualMachineID))
	}
	if err := m.reserveAndReconstructTarget(ctx, record, definition, observedState); err != nil {
		return err
	}
	m.StartTransfer(virtualMachineID)
	return nil
}

// reserveAndReconstructTarget makes the capacity check and its reservation one
// host allocation operation.
func (m *Manager) reserveAndReconstructTarget(
	ctx context.Context,
	record targetRecord,
	definition VirtualMachineDefinition,
	observedState vm.State,
) error {
	unlockAllocation := m.host.LockUserIDAllocation()
	defer unlockAllocation()

	if err := m.reserveShape(ctx, definition.Specification); err != nil {
		return m.recordTargetError(record, err)
	}
	return m.reconstructTarget(record, definition, observedState)
}

// ActiveTargetVirtualMachineIDs returns VM IDs for nonterminal target migrations.
func (m *Manager) ActiveTargetVirtualMachineIDs(_ context.Context) ([]string, error) {
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
		if !record.State.terminal() {
			active = append(active, virtualMachineID)
		}
	}
	return active, nil
}

// isTargetAbandoned reports whether Atlas stopped driving this migration.
// Its caller excludes ready targets because Atlas can already own their VM.
func (m *Manager) isTargetAbandoned(record targetRecord) bool {
	if record.State.terminal() || record.State == targetFinishing ||
		record.State == targetRemovingRuntime || record.State == targetRemovingStorage ||
		record.State == targetRestoringSource || record.State == targetUnlockingSource {
		return false
	}
	return m.now().Sub(record.LastControlAt) > targetIdleTimeout
}

func (m *Manager) reserveShape(ctx context.Context, specification vm.Specification) error {
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

func (m *Manager) reconstructTarget(record targetRecord, definition VirtualMachineDefinition, observedState vm.State) error {
	userID, err := m.reuseOrAllocateUserID(definition.VirtualMachineID)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	desired := vm.DesiredRecord{
		ID:                      definition.VirtualMachineID,
		UserID:                  userID,
		GroupID:                 userID,
		CreateFingerprint:       definition.CreateFingerprint,
		Generation:              definition.Generation,
		SpecificationGeneration: definition.SpecificationGeneration,
		RestartGeneration:       definition.RestartGeneration,
		State:                   definition.DesiredState,
		Specification:           vm.CloneSpecification(definition.Specification),
	}
	observed := vm.ObservedRecord{State: vm.StateUnknown, UpdatedAt: m.now()}
	if err := m.host.WriteDesired(desired); err != nil {
		return m.recordTargetError(record, err)
	}
	if err := m.host.WriteObserved(definition.VirtualMachineID, observed); err != nil {
		return errors.Join(m.recordTargetError(record, err), os.Remove(m.host.DesiredPath(definition.VirtualMachineID)))
	}

	record.Definition = &definition
	record.State = targetCopying
	record.SourceObservedState = observedState
	record.CopyStartedAt = m.now()
	record.Error = nil
	return m.store.writeTarget(record)
}

func (m *Manager) reuseOrAllocateUserID(virtualMachineID string) (uint32, error) {
	if existing, err := m.host.ReadDesired(virtualMachineID); err == nil {
		return existing.UserID, nil
	} else if !errors.Is(err, vm.ErrNotFound) {
		return 0, err
	}
	return m.host.AllocateUserID()
}

func (m *Manager) recordTargetError(record targetRecord, cause error) error {
	record.Error = &vm.OperationError{Code: "migration_error", Message: cause.Error(), UpdatedAt: m.now()}
	if writeError := m.store.writeTarget(record); writeError != nil {
		return errors.Join(cause, writeError)
	}
	return cause
}

// RequestFinish records finish intent for a ready migration. A pending abort wins.
func (m *Manager) RequestFinish(ctx context.Context, migrationID string) error {
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
	if record.State == targetCompleted {
		return nil
	}
	if record.State == targetFinishing {
		return nil
	}
	if record.State != targetReady {
		return vm.ErrConflict
	}
	record.State = targetFinishing
	return m.store.writeTarget(record)
}

// advanceFinish destroys the source and writes a completed record. Each step is
// idempotent, so a retry after the source is gone still completes.
func (m *Manager) advanceFinish(ctx context.Context, record targetRecord) error {
	if err := m.source.FinishSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
		return err
	}
	if err := m.removeReceivedSnapshots(ctx, record); err != nil {
		return err
	}

	_, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *targetRecord) {
		*target = terminalTargetRecord(*target, targetCompleted, m.now())
	})
	return err
}

// removeReceivedSnapshots destroys the migration snapshots left on the received
// dataset after a successful migration. The live volume keeps its data. Repeats
// are safe.
func (m *Manager) removeReceivedSnapshots(ctx context.Context, record targetRecord) error {
	for _, interval := range record.Intervals {
		name := migrationSnapshotName(record.ID, interval.Sequence)
		if err := m.disks.RemoveSnapshot(ctx, record.VirtualMachineID, name); err != nil {
			return err
		}
	}
	return nil
}

// advanceAbort cleans the target, then unlocks or restores the source. Errors
// keep both hosts locked for the next pass.
func (m *Manager) advanceAbort(ctx context.Context, record targetRecord) error {
	record, err := m.cleanAbortTarget(ctx, record)
	if err != nil {
		return err
	}

	// Restore a stopped source before unlocking it.
	if record.State == targetRestoringSource {
		if err := m.source.StartSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
			return err
		}
		if record, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *targetRecord) {
			target.State = targetUnlockingSource
		}); err != nil {
			return err
		}
	}
	if record.State == targetUnlockingSource {
		if err := m.source.RemoveSource(ctx, record.Source, record.ID, record.VirtualMachineID); err != nil {
			return err
		}
	}

	_, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *targetRecord) {
		*target = terminalTargetRecord(*target, targetAborted, m.now())
	})
	return err
}

func (m *Manager) cleanAbortTarget(ctx context.Context, record targetRecord) (targetRecord, error) {
	if record.State == targetRemovingRuntime {
		if err := m.host.RemoveMigratedRuntime(ctx, record.VirtualMachineID); err != nil && !errors.Is(err, vm.ErrNotFound) {
			return record, err
		}
		updated, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *targetRecord) {
			target.State = targetRemovingStorage
		})
		if err != nil {
			return record, err
		}
		record = updated
	}
	if record.State == targetRemovingStorage {
		if err := m.disks.AbortReceive(ctx, record.VirtualMachineID); err != nil {
			return record, err
		}
		if err := m.removeTargetStaging(record.VirtualMachineID); err != nil {
			return record, err
		}
		updated, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *targetRecord) {
			if target.SourceStopped {
				target.State = targetRestoringSource
			} else {
				target.State = targetUnlockingSource
			}
		})
		if err != nil {
			return record, err
		}
		record = updated
	}
	return record, nil
}
