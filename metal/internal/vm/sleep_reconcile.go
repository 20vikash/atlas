package vm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// sleepRequest carries why a VM enters sleep. RequestedAt is when the sleep
// operation started. EligibleAt and LastNetworkActivityAt come from an automatic
// idle decision. They stay zero for a manual warm stop. AbortOnTraffic makes an
// automatic sleep restore the VM when a packet arrives during the warm stop. A
// manual warm stop leaves it false, because the operator asked for the stop.
type sleepRequest struct {
	EligibleAt            time.Time
	RequestedAt           time.Time
	LastNetworkActivityAt time.Time
	// LastNetworkActivityMonotonic is the raw eBPF time of the last packet at the
	// idle decision. The abort check compares it, so wall-clock read jitter never
	// looks like new traffic.
	LastNetworkActivityMonotonic uint64
	AbortOnTraffic               bool
}

// enterSleep warm-stops a live guest and publishes the sleeping observed state.
// It records the sleep-check operation before host work, so an interrupted sleep
// keeps its requested time. It publishes sleeping only after the runtime stops
// with a valid snapshot. It keeps the desired power state.
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

	// Arm the wake before the final idle check. A packet after this point produces
	// a wake event that waits for this VM lock, so no wake is lost during the warm
	// stop. A manual warm stop does not arm, because it wants the VM stopped.
	if request.AbortOnTraffic {
		if err := manager.armNetworkWake(desired); err != nil {
			return err
		}
	}

	before, beforeError := manager.sampleActivityForAbort(ctx, desired, request)

	// A packet since the idle decision aborts the sleep before any snapshot.
	if request.AbortOnTraffic && beforeError == nil && before.LastPacketMonotonicNanoseconds > request.LastNetworkActivityMonotonic {
		return manager.abortSleepBeforeSnapshot(desired, observed)
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

	// A packet during the warm stop aborts an automatic sleep. The just-created
	// snapshot is restored, so the VM keeps running with the new traffic.
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

// abortSleepBeforeSnapshot cancels an automatic sleep that a packet interrupted
// before the warm stop. It disarms the wake and keeps the VM running, so no
// snapshot is made and the new traffic continues.
func (manager *Manager) abortSleepBeforeSnapshot(desired DesiredRecord, observed *ObservedRecord) error {
	manager.disarmNetworkWake(desired)
	manager.logger.Info("automatic sleep aborted by traffic before snapshot",
		"component", "vm", "vm_id", desired.ID)

	observed.Sleep = nil
	observed.State = StateRunning
	observed.completeOperation()
	return manager.store.writeObserved(desired.ID, *observed)
}

// sampleActivityForAbort reads activity only for an automatic sleep. It returns a
// zero value and no error for a manual warm stop, which never aborts on traffic.
func (manager *Manager) sampleActivityForAbort(ctx context.Context, desired DesiredRecord, request sleepRequest) (NetworkActivity, error) {
	if !request.AbortOnTraffic {
		return NetworkActivity{}, nil
	}
	return manager.sampleNetworkActivity(ctx, desired)
}

// recoverInterruptedSleep completes a warm stop that terminated the process but
// did not record the sleeping state. It publishes sleeping from a valid snapshot
// without making a new one. It reports a failure for an invalid snapshot and
// clears the sleep intent when no snapshot published, so the VM boots normally.
func (manager *Manager) recoverInterruptedSleep(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	snapshot, err := manager.runtime.InspectSleepSnapshot(ctx, machine)
	if errors.Is(err, ErrNotFound) {
		// The warm stop did not publish a snapshot. There is nothing to restore,
		// so clear the sleep intent and let the normal flow start the VM.
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
	// InspectSleepSnapshot validated the snapshot against the current desired
	// generations, so it matches the current specification generation.
	observed.Sleep.SpecificationGeneration = desired.SpecificationGeneration
	observed.State = StateSleeping
	observed.Generation = desired.Generation
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}

// reconcileSleeping decides what to do with a VM that is already asleep. It holds
// a VM that still wants sleep, resumes one that wants running or paused, and
// discards the snapshot for a plain stop. It never rewrites the snapshot and
// never cold boots on a resume failure.
func (manager *Manager) reconcileSleeping(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
	status RuntimeStatus,
) error {
	// A running runtime while the record says sleeping means a wake restore ran
	// but did not record the running state. Infer running without loading the
	// snapshot again.
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
		if manager.isSnapshotStale(desired, observed) {
			return manager.coldBootFromSleep(ctx, desired, machine, observed, operationID)
		}
		return manager.resumeFromSleep(ctx, desired, machine, observed, operationID)
	case StatePaused:
		if manager.isSnapshotStale(desired, observed) {
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

// completeWakeWithoutStatus finishes a network wake whose restore ran but whose
// running status was not recorded. It infers running from the live runtime,
// disarms the wake, and publishes running without loading the snapshot again.
func (manager *Manager) completeWakeWithoutStatus(desired DesiredRecord, observed *ObservedRecord) error {
	manager.disarmNetworkWake(desired)

	observed.Sleep = nil
	observed.State = StateRunning
	observed.Generation = desired.Generation
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()
	return manager.store.writeObserved(desired.ID, *observed)
}

// restartFromSleep discards the snapshot and cold boots the guest, so an explicit
// restart of a sleeping VM reboots instead of resuming the saved memory.
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

// isSnapshotStale reports whether a specification shape change since the snapshot
// makes it incompatible. Such a snapshot cannot be resumed and must cold boot.
func (manager *Manager) isSnapshotStale(desired DesiredRecord, observed *ObservedRecord) bool {
	return observed.Sleep != nil && observed.Sleep.SpecificationGeneration != desired.SpecificationGeneration
}

// coldBootFromSleep discards an incompatible snapshot and cold boots the guest
// with the current shape. A later pass applies a pause from the running state.
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

// holdSleeping keeps a VM asleep for another pass. It rearms the wake, so a wake
// that failed to restore is armed again and a later packet notifies again. Arm
// is idempotent. It does not copy generations, so a pending change applies when
// the VM later resumes.
func (manager *Manager) holdSleeping(desired DesiredRecord, observed *ObservedRecord) error {
	if err := manager.armNetworkWake(desired); err != nil {
		return err
	}

	observed.State = StateSleeping
	observed.completeOperation()
	return manager.store.writeObserved(desired.ID, *observed)
}

// loadPausedFromSleep restores a sleeping VM without running the vCPUs and reports
// paused. It keeps the snapshot and the sleeping state on a load failure.
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

// discardSleepToStopped drops the snapshot and reports stopped, so a plain stop
// of a sleeping VM does not resume it.
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

// resumeFromSleep restores a sleeping VM from its snapshot and reports running.
// It keeps the snapshot and the sleeping state on a load failure, so a bad
// snapshot never cold boots. It does not copy the desired generation, so a
// pending stop, pause, or specification change applies on the next pass.
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
