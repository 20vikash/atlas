package vm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// sleepRequest records why a VM enters sleep.
type sleepRequest struct {
	EligibleAt            time.Time
	RequestedAt           time.Time
	LastNetworkActivityAt time.Time
	// LastNetworkActivityMonotonic is the packet time at the idle decision.
	LastNetworkActivityMonotonic uint64
	AbortOnTraffic               bool
}

// enterSleep warm-stops a live guest and publishes sleeping state.
func (manager *Manager) enterSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
	request sleepRequest,
) error {
	observed.Sleep = &SleepProgress{
		EligibleAt:            request.EligibleAt,
		RequestedAt:           request.RequestedAt,
		LastNetworkActivityAt: request.LastNetworkActivityAt,
	}
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseSleepCheck, func() error {
		return nil
	}); err != nil {
		return err
	}

	// Arm wake before the final idle check for automatic sleep.
	if request.AbortOnTraffic {
		if err := manager.armNetworkWake(desired); err != nil {
			return err
		}
	}

	before, beforeError := manager.sampleActivityForAbort(ctx, desired, request)

	// A packet since the idle decision aborts the sleep before any snapshot.
	if request.AbortOnTraffic && beforeError == nil && before.LastPacketMonotonicNanoseconds > request.LastNetworkActivityMonotonic {
		return manager.abortSleepBeforeMemorySnapshot(desired, observed)
	}

	var outcome StopOutcome
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseSleepSnapshot, func() error {
		var stopError error
		outcome, stopError = manager.runtime.Stop(ctx, machine, StopWithSleepSnapshot)
		return stopError
	}); err != nil {
		return err
	}
	if outcome.MemorySnapshotGeneration == 0 {
		return fmt.Errorf("warm stop for VM %s published no snapshot generation", desired.ID)
	}
	observed.Sleep.MemorySnapshotGeneration = outcome.MemorySnapshotGeneration
	observed.Sleep.MemorySnapshotCreatedAt = outcome.MemorySnapshotCreatedAt
	observed.Sleep.SpecificationGeneration = desired.SpecificationGeneration

	// A packet during the warm stop aborts automatic sleep.
	after, afterError := manager.sampleActivityForAbort(ctx, desired, request)
	if request.AbortOnTraffic && beforeError == nil && afterError == nil && after.LastPacketMonotonicNanoseconds > before.LastPacketMonotonicNanoseconds {
		manager.logger.Info("automatic sleep aborted by traffic",
			"component", "vm", "vm_id", desired.ID)
		return manager.resumeFromSleep(ctx, desired, machine, observed, operationID)
	}

	observed.State = StateSleeping
	observed.Generation = desired.Generation
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// abortSleepBeforeMemorySnapshot cancels automatic sleep before snapshotting.
func (manager *Manager) abortSleepBeforeMemorySnapshot(desired DesiredRecord, observed *ObservedRecord) error {
	manager.disarmNetworkWake(desired)
	manager.logger.Info("automatic sleep aborted by traffic before snapshot",
		"component", "vm", "vm_id", desired.ID)

	observed.Sleep = nil
	observed.State = StateRunning
	observed.completeOperation()
	return manager.store.writeObserved(desired.ID, *observed)
}

// sampleActivityForAbort reads activity for automatic sleep.
func (manager *Manager) sampleActivityForAbort(ctx context.Context, desired DesiredRecord, request sleepRequest) (NetworkActivity, error) {
	if !request.AbortOnTraffic {
		return NetworkActivity{}, nil
	}
	return manager.sampleNetworkActivity(ctx, desired)
}

// recoverInterruptedSleep completes a warm stop that missed its sleeping state.
func (manager *Manager) recoverInterruptedSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	snapshot, err := manager.runtime.InspectSleepSnapshot(ctx, machine)
	if errors.Is(err, ErrNotFound) {
		// No snapshot means normal startup can continue.
		observed.Sleep = nil
		observed.Phase = ""
		observed.OperationID = ""
		return manager.store.writeObserved(desired.ID, *observed)
	}
	if err != nil {
		// The snapshot is present but invalid. Keep the evidence and report it.
		return manager.runOperation(ctx, desired.ID, observed, operationID, phaseSleepSnapshot, func() error {
			return fmt.Errorf("recover sleep snapshot for VM %s: %w", desired.ID, err)
		})
	}

	if observed.Sleep == nil {
		observed.Sleep = &SleepProgress{}
	}
	observed.Sleep.MemorySnapshotGeneration = snapshot.Generation
	observed.Sleep.MemorySnapshotCreatedAt = snapshot.CreatedAt
	// The validated snapshot matches the current shape.
	observed.Sleep.SpecificationGeneration = desired.SpecificationGeneration
	observed.State = StateSleeping
	observed.Generation = desired.Generation
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// reconcileSleeping applies desired state to a sleeping VM.
func (manager *Manager) reconcileSleeping(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
	status RuntimeStatus,
) error {
	// A running runtime means wake restore completed before its status write.
	if status.State == StateRunning {
		return manager.completeWakeWithoutStatus(desired, observed)
	}

	// A restart wants a fresh guest, so discard the snapshot and cold reboot.
	if observed.RestartGeneration < desired.RestartGeneration {
		return manager.restartFromSleep(ctx, desired, machine, observed, operationID)
	}

	caughtUp := observed.Generation == desired.Generation && observed.RestartGeneration == desired.RestartGeneration

	switch desired.State {
	case StateRunning:
		if desired.Specification.IsSleepy && caughtUp {
			return manager.holdSleeping(desired, observed)
		}
		if manager.isMemorySnapshotStale(desired, observed) {
			return manager.coldBootFromSleep(ctx, desired, machine, observed, operationID)
		}
		return manager.resumeFromSleep(ctx, desired, machine, observed, operationID)
	case StatePaused:
		if manager.isMemorySnapshotStale(desired, observed) {
			return manager.coldBootFromSleep(ctx, desired, machine, observed, operationID)
		}
		return manager.loadPausedFromSleep(ctx, desired, machine, observed, operationID)
	case StateStopped:
		if desired.WarmStop {
			return manager.holdSleeping(desired, observed)
		}
		return manager.discardSleepToStopped(ctx, desired, machine, observed, operationID)
	default:
		return &TransitionError{DesiredState: desired.State, ObservedState: StateSleeping}
	}
}

// completeWakeWithoutStatus records a wake restore that already completed.
func (manager *Manager) completeWakeWithoutStatus(desired DesiredRecord, observed *ObservedRecord) error {
	manager.disarmNetworkWake(desired)

	observed.Sleep = nil
	observed.State = StateRunning
	observed.Generation = desired.Generation
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()
	return manager.store.writeObserved(desired.ID, *observed)
}

// restartFromSleep discards the snapshot and cold boots the guest.
func (manager *Manager) restartFromSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseRestart, func() error {
		if err := manager.runtime.DiscardSleepSnapshot(ctx, machine); err != nil {
			return err
		}
		return manager.runtime.Start(ctx, machine, StartNormal)
	}); err != nil {
		return err
	}

	manager.disarmNetworkWake(desired)
	observed.Sleep = nil
	observed.State = StateRunning
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// isMemorySnapshotStale reports whether the snapshot shape is incompatible.
func (manager *Manager) isMemorySnapshotStale(desired DesiredRecord, observed *ObservedRecord) bool {
	return observed.Sleep != nil && observed.Sleep.SpecificationGeneration != desired.SpecificationGeneration
}

// coldBootFromSleep discards an incompatible snapshot and cold boots the guest.
func (manager *Manager) coldBootFromSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseStart, func() error {
		if err := manager.runtime.DiscardSleepSnapshot(ctx, machine); err != nil {
			return err
		}
		return manager.runtime.Start(ctx, machine, StartNormal)
	}); err != nil {
		return err
	}

	manager.disarmNetworkWake(desired)
	observed.Sleep = nil
	observed.State = StateRunning
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// holdSleeping keeps a VM asleep and rearms packet wake.
func (manager *Manager) holdSleeping(desired DesiredRecord, observed *ObservedRecord) error {
	if err := manager.armNetworkWake(desired); err != nil {
		return err
	}

	observed.State = StateSleeping
	observed.completeOperation()
	return manager.store.writeObserved(desired.ID, *observed)
}

// loadPausedFromSleep restores a sleeping VM without running its vCPUs.
func (manager *Manager) loadPausedFromSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseStart, func() error {
		return manager.runtime.Start(ctx, machine, StartFromSleepSnapshotPaused)
	}); err != nil {
		return err
	}

	manager.disarmNetworkWake(desired)
	observed.Sleep = nil
	observed.State = StatePaused
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// discardSleepToStopped drops the snapshot and reports stopped.
func (manager *Manager) discardSleepToStopped(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseStop, func() error {
		return manager.runtime.DiscardSleepSnapshot(ctx, machine)
	}); err != nil {
		return err
	}

	manager.disarmNetworkWake(desired)
	observed.Sleep = nil
	observed.State = StateStopped
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// resumeFromSleep restores a sleeping VM from its snapshot.
func (manager *Manager) resumeFromSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseStart, func() error {
		return manager.runtime.Start(ctx, machine, StartFromSleepSnapshot)
	}); err != nil {
		return err
	}

	manager.disarmNetworkWake(desired)
	observed.Sleep = nil
	observed.State = StateRunning
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}
