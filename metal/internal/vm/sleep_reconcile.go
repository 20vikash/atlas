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
	AbortOnTraffic        bool
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

	before, beforeError := manager.sampleActivityForAbort(ctx, desired, request)

	var outcome StopOutcome
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseSleepSnapshot, func() error {
		var stopError error
		outcome, stopError = manager.runtime.Stop(ctx, machine, StopWithSleepSnapshot)
		return stopError
	}); err != nil {
		return err
	}
	if outcome.SnapshotGeneration == 0 {
		return fmt.Errorf("warm stop for VM %s published no snapshot generation", desired.ID)
	}
	observed.Sleep.SnapshotGeneration = outcome.SnapshotGeneration
	observed.Sleep.SnapshotCreatedAt = outcome.SnapshotCreatedAt

	// A packet during the warm stop aborts an automatic sleep. The just-created
	// snapshot is restored, so the VM keeps running with the new traffic.
	after, afterError := manager.sampleActivityForAbort(ctx, desired, request)
	if request.AbortOnTraffic && beforeError == nil && afterError == nil && after.LastSeenAt.After(before.LastSeenAt) {
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
	observed.Sleep.SnapshotGeneration = snapshot.Generation
	observed.Sleep.SnapshotCreatedAt = snapshot.CreatedAt
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
) error {
	caughtUp := observed.Generation == desired.Generation && observed.RestartGeneration == desired.RestartGeneration

	switch desired.State {
	case StateRunning:
		if desired.Specification.IsSleepy && caughtUp {
			return manager.holdSleeping(desired, observed)
		}
		return manager.resumeFromSleep(ctx, desired, machine, observed, operationID)
	case StatePaused:
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

// holdSleeping keeps a VM asleep for another pass. It does not copy generations,
// so a pending change applies when the VM later resumes.
func (manager *Manager) holdSleeping(desired DesiredRecord, observed *ObservedRecord) error {
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

	observed.Sleep = nil
	observed.State = StateRunning
	observed.completeOperation()

	return manager.store.writeObserved(desired.ID, *observed)
}
