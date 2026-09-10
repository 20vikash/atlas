package vm

import (
	"context"
	"errors"
)

// SourceHandshake is the portable state that the source returns to the target.
type SourceHandshake struct {
	Config        PortableConfig
	ObservedState State
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

// UnlockSource removes the source migration state and unlocks the VM. It does
// not delete the VM. It is safe to repeat.
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
	return m.store.remove(virtualMachineID)
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
