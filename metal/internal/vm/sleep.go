package vm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Sleep eligibility reasons name the outcome of one idle check. They appear in
// logs and tests, so a reviewer can see why a VM did or did not become eligible.
const (
	sleepReasonEligible          = "idle timeout reached"
	sleepReasonDisabled          = "automatic sleep is disabled"
	sleepReasonNotDesiredRunning = "desired state is not running"
	sleepReasonNotSleepy         = "VM is not sleepy"
	sleepReasonGenerationPending = "desired generation is pending"
	sleepReasonRestartPending    = "restart generation is pending"
	sleepReasonRuntimeNotRunning = "runtime is not running"
	sleepReasonOperationPending  = "a VM operation is incomplete"
	sleepReasonNoBaseline        = "activity monitor has no baseline"
	sleepReasonNotIdle           = "activity is within the idle timeout"
)

// sleepEligibility is the outcome of one idle check. Activity is the sampled
// value the decision used. Reason names the rule that decided the result.
type sleepEligibility struct {
	Eligible bool
	Reason   string
	Activity NetworkActivity
}

// sampleNetworkActivity reads the last packet time of one VM. The caller holds
// the VM operation lock. A read failure is wrapped with VM context and stays
// retryable. A missing attachment keeps vm.ErrNotFound for the caller to detect.
func (manager *Manager) sampleNetworkActivity(ctx context.Context, desired DesiredRecord) (NetworkActivity, error) {
	request := NetworkActivityRequest{VirtualMachineID: desired.ID, UserID: desired.UserID}
	activity, err := manager.networkActivityMonitor.LastNetworkActivity(ctx, request)
	if err != nil {
		return NetworkActivity{}, fmt.Errorf("read network activity for VM %s: %w", desired.ID, err)
	}
	return activity, nil
}

// evaluateSleepEligibility decides whether one VM can enter automatic sleep. It
// applies every eligibility rule and never stops the VM. The caller holds the VM
// operation lock and passes the current time as now. It returns a retryable error
// only for a network read failure. A missing attachment is not eligible, not an
// error, so a VM whose monitor has no baseline stays awake.
func (manager *Manager) evaluateSleepEligibility(
	ctx context.Context,
	desired DesiredRecord,
	observed ObservedRecord,
	status RuntimeStatus,
	now time.Time,
) (sleepEligibility, error) {
	sleep := manager.configuration.Sleep
	if !sleep.Enabled || sleep.IdleTimeout <= 0 {
		return sleepEligibility{Reason: sleepReasonDisabled}, nil
	}
	if desired.State != StateRunning {
		return sleepEligibility{Reason: sleepReasonNotDesiredRunning}, nil
	}
	if !desired.Specification.IsSleepy {
		return sleepEligibility{Reason: sleepReasonNotSleepy}, nil
	}
	if observed.Generation != desired.Generation {
		return sleepEligibility{Reason: sleepReasonGenerationPending}, nil
	}
	if observed.RestartGeneration != desired.RestartGeneration {
		return sleepEligibility{Reason: sleepReasonRestartPending}, nil
	}
	if status.State != StateRunning {
		return sleepEligibility{Reason: sleepReasonRuntimeNotRunning}, nil
	}
	if observed.Error != nil {
		return sleepEligibility{Reason: sleepReasonOperationPending}, nil
	}

	activity, err := manager.sampleNetworkActivity(ctx, desired)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return sleepEligibility{Reason: sleepReasonNoBaseline}, nil
		}
		return sleepEligibility{}, err
	}
	// LastSeenAt already carries the attachment baseline when HasBeenSeen is
	// false, so one age calculation covers a VM that has seen no packet yet.
	if now.Sub(activity.LastSeenAt) < sleep.IdleTimeout {
		return sleepEligibility{Reason: sleepReasonNotIdle, Activity: activity}, nil
	}
	return sleepEligibility{Eligible: true, Reason: sleepReasonEligible, Activity: activity}, nil
}
