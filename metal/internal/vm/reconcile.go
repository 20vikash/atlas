package vm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	phaseInspect        = "inspect"
	phaseNetwork        = "network"
	phaseStorage        = "storage"
	phaseStart          = "start"
	phaseStop           = "stop"
	phasePause          = "pause"
	phaseResume         = "resume"
	phaseRestart        = "restart"
	phaseMetadata       = "metadata"
	phaseDisk           = "disk"
	phaseSnapshot       = "snapshot"
	phaseDestroyRuntime = "destroy-runtime"
	phaseDestroyNetwork = "destroy-network"
	phaseDestroyStorage = "destroy-storage"
)

// Reconcile moves observed VM state toward the latest desired record.
func (manager *Manager) Reconcile(ctx context.Context, identifier string) error {
	virtualMachine := manager.newMachine(identifier)
	unlock, err := virtualMachine.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	desired, observed, err := virtualMachine.records()
	if err != nil {
		return err
	}
	operationID := newOperationID()
	if desired.State == StateDestroyed {
		return manager.reconcileDestroyed(ctx, desired, observed, operationID)
	}
	return manager.reconcileActive(ctx, desired, observed, operationID)
}

func (manager *Manager) reconcileActive(
	ctx context.Context,
	desired DesiredRecord,
	observed ObservedRecord,
	operationID string,
) error {
	var interfaceState NetworkInterface
	err := manager.runOperation(ctx, desired.ID, &observed, operationID, phaseNetwork, func() error {
		var ensureError error
		interfaceState, ensureError = manager.network.Ensure(ctx, networkRequest(desired))
		return ensureError
	})
	if err != nil {
		return err
	}
	observed.NetworkInterface = interfaceState
	machine := runtimeMachine(desired, interfaceState)
	status, err := manager.inspect(ctx, desired.ID, machine, &observed, operationID)
	if err != nil {
		return err
	}

	if observed.RestartGeneration < desired.RestartGeneration {
		status, err = manager.applyRestart(ctx, desired.ID, machine, status, &observed, operationID)
		if err != nil {
			return err
		}
		observed.RestartGeneration = desired.RestartGeneration
	}
	status, err = manager.applyDesiredState(ctx, desired.ID, machine, status, desired.State, &observed, operationID)
	if err != nil {
		return err
	}
	if desired.Generation != observed.Generation {
		if err := manager.applyDesiredSpecification(ctx, desired, machine, status, &observed, operationID); err != nil {
			return err
		}
		observed.Generation = desired.Generation
	}
	if err := manager.runOperation(ctx, desired.ID, &observed, operationID, phaseStorage, func() error {
		var usageError error
		observed.Disk, usageError = manager.storage.DiskUsage(ctx, desired.ID)
		return usageError
	}); err != nil {
		return err
	}

	observed.State = status.State
	observed.Phase = ""
	observed.OperationID = ""
	observed.OperationStartedAt = time.Time{}
	observed.Error = nil
	observed.UpdatedAt = time.Now().UTC()
	return manager.store.writeObserved(desired.ID, observed)
}

func (manager *Manager) inspect(
	ctx context.Context,
	identifier string,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) (RuntimeStatus, error) {
	var status RuntimeStatus
	err := manager.runOperation(ctx, identifier, observed, operationID, phaseInspect, func() error {
		var inspectError error
		status, inspectError = manager.runtime.Inspect(ctx, machine)
		return inspectError
	})
	if err != nil {
		observed.State = StateUnknown
		_ = manager.store.writeObserved(identifier, *observed)
	}
	return status, err
}

func (manager *Manager) applyRestart(
	ctx context.Context,
	identifier string,
	machine RuntimeMachine,
	status RuntimeStatus,
	observed *ObservedRecord,
	operationID string,
) (RuntimeStatus, error) {
	if status.State != StateRunning && status.State != StatePaused {
		return status, nil
	}
	err := manager.runOperation(ctx, identifier, observed, operationID, phaseRestart, func() error {
		if err := manager.runtime.Stop(ctx, machine); err != nil {
			return err
		}
		return manager.runtime.Start(ctx, machine)
	})
	if err != nil {
		return status, err
	}
	return RuntimeStatus{State: StateRunning}, nil
}

func (manager *Manager) applyDesiredState(
	ctx context.Context,
	identifier string,
	machine RuntimeMachine,
	status RuntimeStatus,
	desiredState State,
	observed *ObservedRecord,
	operationID string,
) (RuntimeStatus, error) {
	switch desiredState {
	case StateRunning:
		if status.State == StatePaused {
			return manager.runRuntimeTransition(ctx, identifier, machine, observed, operationID, phaseResume, StateRunning, manager.runtime.Resume)
		}
		if status.State != StateRunning {
			return manager.runRuntimeTransition(ctx, identifier, machine, observed, operationID, phaseStart, StateRunning, manager.runtime.Start)
		}
	case StatePaused:
		if status.State != StateRunning && status.State != StatePaused {
			if _, err := manager.runRuntimeTransition(ctx, identifier, machine, observed, operationID, phaseStart, StateRunning, manager.runtime.Start); err != nil {
				return status, err
			}
			status.State = StateRunning
		}
		if status.State == StateRunning {
			return manager.runRuntimeTransition(ctx, identifier, machine, observed, operationID, phasePause, StatePaused, manager.runtime.Pause)
		}
	case StateStopped:
		if status.State != StateStopped {
			return manager.runRuntimeTransition(ctx, identifier, machine, observed, operationID, phaseStop, StateStopped, manager.runtime.Stop)
		}
	default:
		return status, &TransitionError{DesiredState: desiredState, ObservedState: status.State}
	}
	return status, nil
}

func (manager *Manager) runRuntimeTransition(
	ctx context.Context,
	identifier string,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
	phase string,
	result State,
	operation func(context.Context, RuntimeMachine) error,
) (RuntimeStatus, error) {
	err := manager.runOperation(ctx, identifier, observed, operationID, phase, func() error {
		return operation(ctx, machine)
	})
	return RuntimeStatus{State: result}, err
}

func (manager *Manager) applyDesiredSpecification(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	status RuntimeStatus,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseStorage, func() error {
		return manager.storage.ResizeDisk(ctx, desired.ID, desired.Specification.DiskMiB)
	}); err != nil {
		return err
	}
	if status.State == StateRunning || status.State == StatePaused {
		if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseDisk, func() error {
			return manager.runtime.RefreshDisk(ctx, machine)
		}); err != nil {
			return err
		}
		if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseMetadata, func() error {
			return manager.runtime.RefreshMetadata(ctx, machine)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager) reconcileDestroyed(
	ctx context.Context,
	desired DesiredRecord,
	observed ObservedRecord,
	operationID string,
) error {
	machine := runtimeMachine(desired, NetworkInterface{})
	steps := []struct {
		complete *bool
		phase    string
		run      func() error
	}{
		{&observed.RuntimeCleanupComplete, phaseDestroyRuntime, func() error { return manager.runtime.Remove(ctx, machine) }},
		{&observed.NetworkCleanupComplete, phaseDestroyNetwork, func() error {
			return manager.network.Release(ctx, NetworkReleaseRequest{
				VirtualMachineID:  desired.ID,
				UserID:            desired.UserID,
				WireGuardMeshIPv6: desired.Specification.Network.WireGuardMeshIPv6,
			})
		}},
		{&observed.StorageCleanupComplete, phaseDestroyStorage, func() error { return manager.storage.Release(ctx, desired.ID) }},
	}
	for _, step := range steps {
		if *step.complete {
			continue
		}
		if err := manager.runOperation(ctx, desired.ID, &observed, operationID, step.phase, step.run); err != nil {
			return err
		}
		*step.complete = true
		observed.State = StateDestroyed
		if err := manager.store.writeObserved(desired.ID, observed); err != nil {
			return err
		}
	}
	if err := manager.store.remove(desired.ID); err != nil {
		return fmt.Errorf("remove virtual machine records: %w", err)
	}
	return nil
}

func (manager *Manager) runOperation(
	ctx context.Context,
	identifier string,
	observed *ObservedRecord,
	operationID string,
	phase string,
	operation func() error,
) error {
	now := time.Now().UTC()
	observed.Phase = phase
	if observed.OperationID != operationID {
		observed.OperationStartedAt = now
	}
	observed.OperationID = operationID
	observed.UpdatedAt = now
	observed.Error = nil
	if err := manager.store.writeObserved(identifier, *observed); err != nil {
		manager.logOperationFailure(ctx, identifier, observed, phase, now, err)
		return err
	}
	if err := operation(); err != nil {
		observed.Error = &OperationError{
			Code: "operation_failed", Message: phase + " operation failed",
			LocalDetail: err.Error(), UpdatedAt: time.Now().UTC(),
		}
		observed.UpdatedAt = observed.Error.UpdatedAt
		if writeError := manager.store.writeObserved(identifier, *observed); writeError != nil {
			manager.logOperationFailure(ctx, identifier, observed, phase, now, errors.Join(err, writeError))
			return fmt.Errorf("%s: %w", phase, writeError)
		}
		manager.logOperationFailure(ctx, identifier, observed, phase, now, err)
		return fmt.Errorf("%s: %w", phase, err)
	}
	manager.logger.Info("virtual machine operation completed",
		"component", "vm", "operation", "reconcile", "phase", phase, "vm_id", identifier,
		"operation_id", operationID, "applied_generation", observed.Generation,
		"observed_state", observed.State, "duration", time.Since(now),
	)
	return nil
}

func (manager *Manager) logOperationFailure(
	ctx context.Context,
	identifier string,
	observed *ObservedRecord,
	phase string,
	startedAt time.Time,
	err error,
) {
	manager.logger.ErrorContext(ctx, "virtual machine operation failed",
		"component", "vm", "operation", "reconcile", "phase", phase, "vm_id", identifier,
		"operation_id", observed.OperationID, "applied_generation", observed.Generation,
		"observed_state", observed.State, "duration", time.Since(startedAt), "error", err,
	)
}
