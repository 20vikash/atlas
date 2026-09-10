package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// AdvanceTarget runs the handshake for a preparing migration, then starts or
// resumes the disk transfer. A copying migration only starts or resumes the
// transfer. It returns at once and never runs the transfer itself.
func (m *MigrationManager) AdvanceTarget(ctx context.Context, virtualMachineID string) error {
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
	if record.Status != MigrationRunning {
		return nil
	}
	if record.Phase == PhaseCopying {
		m.StartTransfer(virtualMachineID)
		return nil
	}
	if m.now().Sub(record.CreatedAt) > reservationTimeout {
		m.logger.Warn("migration target expired before the handshake started", "migration_id", record.ID)
		return m.abortTargetLocked(ctx, record)
	}

	token, err := m.store.readToken(virtualMachineID)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	config, observedState, err := m.source.PrepareSource(ctx, record.Source, record.ID, virtualMachineID, token)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	if config.VirtualMachineID != virtualMachineID {
		return m.recordTargetError(record, fmt.Errorf("source returned config for %s", config.VirtualMachineID))
	}
	if err := m.reserveShape(ctx, config.Specification); err != nil {
		return m.recordTargetError(record, err)
	}
	if err := m.reconstructTarget(record, config, observedState); err != nil {
		return err
	}
	m.StartTransfer(virtualMachineID)
	return nil
}

// ActiveTargetVirtualMachineIDs returns the VM IDs of running target migrations.
func (m *MigrationManager) ActiveTargetVirtualMachineIDs(_ context.Context) ([]string, error) {
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
		if record.Status == MigrationRunning {
			active = append(active, virtualMachineID)
		}
	}
	return active, nil
}

// reserveShape rejects a migration that the current host capacity cannot hold.
func (m *MigrationManager) reserveShape(ctx context.Context, specification Specification) error {
	available, err := m.capacity(ctx)
	if err != nil {
		return err
	}
	if specification.VirtualCPUCount > available.CPUCount ||
		specification.MemoryMiB > available.MemoryMiB ||
		specification.DiskMiB > available.StorageMiB {
		return ErrConflict
	}
	return nil
}

// reconstructTarget writes fresh target records with local IDs and moves the
// migration to the copying phase. It reuses a placeholder from a prior attempt.
func (m *MigrationManager) reconstructTarget(record TargetMigrationRecord, config PortableConfig, observedState State) error {
	m.machines.allocationMutex.Lock()
	defer m.machines.allocationMutex.Unlock()

	userID, err := m.reuseOrAllocateUserID(config.VirtualMachineID)
	if err != nil {
		return m.recordTargetError(record, err)
	}
	desired := DesiredRecord{
		ID:                      config.VirtualMachineID,
		UserID:                  userID,
		GroupID:                 userID,
		CreateFingerprint:       config.CreateFingerprint,
		Generation:              config.Generation,
		SpecificationGeneration: config.SpecificationGeneration,
		RestartGeneration:       config.RestartGeneration,
		State:                   config.DesiredState,
		Specification:           cloneSpecification(config.Specification),
	}
	observed := ObservedRecord{State: StateUnknown, UpdatedAt: m.now()}
	if err := m.machines.store.writeDesired(desired); err != nil {
		return m.recordTargetError(record, err)
	}
	if err := m.machines.store.writeObserved(config.VirtualMachineID, observed); err != nil {
		return errors.Join(m.recordTargetError(record, err), os.Remove(m.machines.store.desiredPath(config.VirtualMachineID)))
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

// reuseOrAllocateUserID keeps the local ID of a placeholder from a prior attempt.
func (m *MigrationManager) reuseOrAllocateUserID(virtualMachineID string) (uint32, error) {
	if existing, err := m.machines.store.readDesired(virtualMachineID); err == nil {
		return existing.UserID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	return m.machines.allocateUserID()
}

// recordTargetError stores a visible error and returns the cause.
func (m *MigrationManager) recordTargetError(record TargetMigrationRecord, cause error) error {
	record.Error = &OperationError{Code: "migration_error", Message: cause.Error(), UpdatedAt: m.now()}
	if writeError := m.store.writeTarget(record); writeError != nil {
		return errors.Join(cause, writeError)
	}
	return cause
}
