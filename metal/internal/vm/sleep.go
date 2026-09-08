package vm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Sleep eligibility reasons name the outcome of an idle check.
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

// sleepEligibility is the result of an idle check.
type sleepEligibility struct {
	Eligible bool
	Reason   string
	Activity NetworkActivity
}

// sampleNetworkActivity reads one VM's last packet time.
func (manager *Manager) sampleNetworkActivity(ctx context.Context, desired DesiredRecord) (NetworkActivity, error) {
	request := NetworkActivityRequest{VirtualMachineID: desired.ID, UserID: desired.UserID}
	activity, err := manager.networkActivityMonitor.LastNetworkActivity(ctx, request)
	if err != nil {
		return NetworkActivity{}, fmt.Errorf("read network activity for VM %s: %w", desired.ID, err)
	}
	return activity, nil
}

// evaluateSleepEligibility applies the automatic sleep rules without stopping.
func (manager *Manager) evaluateSleepEligibility(
	ctx context.Context,
	desired DesiredRecord,
	observed ObservedRecord,
	status RuntimeStatus,
	now time.Time,
) (sleepEligibility, error) {
	policy := desired.Sleep
	if !policy.IsSleepy {
		return sleepEligibility{Reason: sleepReasonNotSleepy}, nil
	}
	if policy.IdleTimeoutSeconds <= 0 {
		return sleepEligibility{Reason: sleepReasonDisabled}, nil
	}
	if desired.State != StateRunning {
		return sleepEligibility{Reason: sleepReasonNotDesiredRunning}, nil
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
	// LastSeenAt is also the baseline before the first packet.
	if now.Sub(activity.LastSeenAt) < policy.IdleTimeout() {
		return sleepEligibility{Reason: sleepReasonNotIdle, Activity: activity}, nil
	}
	return sleepEligibility{Eligible: true, Reason: sleepReasonEligible, Activity: activity}, nil
}
