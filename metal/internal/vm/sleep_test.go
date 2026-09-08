package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// buildSleepManager returns a manager with controllable activity.
func buildSleepManager(t *testing.T, monitor NetworkActivityMonitor) *Manager {
	t.Helper()
	manager, err := NewManager(
		ManagerConfig{
			MachinesDirectory: t.TempDir(),
			UserIDRange:       UserIDRange{Min: 1000, Max: 1010},
		},
		ManagerDependencies{
			Runtime:                &fakeRuntime{},
			Network:                &fakeNetwork{},
			Storage:                &fakeStorage{},
			Snapshots:              fakeSnapshots{},
			NetworkActivityMonitor: monitor,
			NetworkWakeMonitor:     &fakeNetworkWakeMonitor{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func sleepDesired() DesiredRecord {
	return DesiredRecord{
		ID:                "machine-1",
		UserID:            1000,
		Generation:        3,
		RestartGeneration: 1,
		State:             StateRunning,
		Sleep:             SleepPolicy{IsSleepy: true, IdleTimeoutSeconds: 1800},
	}
}

func sleepObserved() ObservedRecord {
	return ObservedRecord{Generation: 3, RestartGeneration: 1}
}

func TestNewManagerRequiresTheNetworkMonitors(t *testing.T) {
	monitor := &fakeNetworkActivityMonitor{}
	wake := &fakeNetworkWakeMonitor{}
	cases := []struct {
		name    string
		monitor NetworkActivityMonitor
		wake    NetworkWakeMonitor
		wantErr bool
	}{
		{name: "an activity monitor is required", monitor: nil, wake: wake, wantErr: true},
		{name: "a wake monitor is required", monitor: monitor, wake: nil, wantErr: true},
		{name: "both are accepted", monitor: monitor, wake: wake, wantErr: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewManager(
				ManagerConfig{MachinesDirectory: t.TempDir()},
				ManagerDependencies{
					Runtime:                &fakeRuntime{},
					Network:                &fakeNetwork{},
					Storage:                &fakeStorage{},
					Snapshots:              fakeSnapshots{},
					NetworkActivityMonitor: testCase.monitor,
					NetworkWakeMonitor:     testCase.wake,
				},
			)
			if testCase.wantErr != (err != nil) {
				t.Fatalf("error = %v, want error %t", err, testCase.wantErr)
			}
		})
	}
}

func TestSampleNetworkActivityBuildsRequestFromRecord(t *testing.T) {
	monitor := &fakeNetworkActivityMonitor{activity: NetworkActivity{HasBeenSeen: true}}
	manager := buildSleepManager(t, monitor)
	desired := sleepDesired()

	if _, err := manager.sampleNetworkActivity(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	if monitor.lastRequest.VirtualMachineID != desired.ID || monitor.lastRequest.UserID != desired.UserID {
		t.Fatalf("request = %+v, want VM %s user %d", monitor.lastRequest, desired.ID, desired.UserID)
	}
}

func TestSampleNetworkActivityWrapsErrors(t *testing.T) {
	// A missing attachment keeps ErrNotFound. A read failure is wrapped and named.
	notFound := &fakeNetworkActivityMonitor{err: fmt.Errorf("activity for VM machine-1: %w", ErrNotFound)}
	manager := buildSleepManager(t, notFound)
	if _, err := manager.sampleNetworkActivity(context.Background(), sleepDesired()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}

	readFailure := &fakeNetworkActivityMonitor{err: errors.New("map read failed")}
	manager = buildSleepManager(t, readFailure)
	_, err := manager.sampleNetworkActivity(context.Background(), sleepDesired())
	if err == nil || !strings.Contains(err.Error(), "machine-1") {
		t.Fatalf("error = %v, want a wrapped VM read failure", err)
	}
}

func TestSleepEligibilityRules(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timeout := 30 * time.Minute
	idle := NetworkActivity{LastSeenAt: now.Add(-2 * timeout), HasBeenSeen: true}
	recent := NetworkActivity{LastSeenAt: now.Add(-time.Minute), HasBeenSeen: true}

	cases := []struct {
		name         string
		desired      func(DesiredRecord) DesiredRecord
		observed     func(ObservedRecord) ObservedRecord
		status       RuntimeStatus
		activity     NetworkActivity
		activityErr  error
		wantEligible bool
		wantReason   string
		wantErr      bool
	}{
		{
			name:         "idle sleepy VM is eligible",
			status:       RuntimeStatus{State: StateRunning},
			activity:     idle,
			wantEligible: true,
			wantReason:   sleepReasonEligible,
		},
		{
			name:       "no idle timeout disables sleep",
			desired:    func(d DesiredRecord) DesiredRecord { d.Sleep.IdleTimeoutSeconds = 0; return d },
			status:     RuntimeStatus{State: StateRunning},
			activity:   idle,
			wantReason: sleepReasonDisabled,
		},
		{
			name:       "not running desired state",
			desired:    func(d DesiredRecord) DesiredRecord { d.State = StateStopped; return d },
			status:     RuntimeStatus{State: StateRunning},
			activity:   idle,
			wantReason: sleepReasonNotDesiredRunning,
		},
		{
			name:       "not sleepy",
			desired:    func(d DesiredRecord) DesiredRecord { d.Sleep.IsSleepy = false; return d },
			status:     RuntimeStatus{State: StateRunning},
			activity:   idle,
			wantReason: sleepReasonNotSleepy,
		},
		{
			name:       "specification generation pending",
			observed:   func(o ObservedRecord) ObservedRecord { o.Generation = 2; return o },
			status:     RuntimeStatus{State: StateRunning},
			activity:   idle,
			wantReason: sleepReasonGenerationPending,
		},
		{
			name:       "restart generation pending",
			observed:   func(o ObservedRecord) ObservedRecord { o.RestartGeneration = 0; return o },
			status:     RuntimeStatus{State: StateRunning},
			activity:   idle,
			wantReason: sleepReasonRestartPending,
		},
		{
			name:       "runtime not running",
			status:     RuntimeStatus{State: StatePaused},
			activity:   idle,
			wantReason: sleepReasonRuntimeNotRunning,
		},
		{
			name:       "incomplete operation",
			observed:   func(o ObservedRecord) ObservedRecord { o.Error = &OperationError{Code: "operation_failed"}; return o },
			status:     RuntimeStatus{State: StateRunning},
			activity:   idle,
			wantReason: sleepReasonOperationPending,
		},
		{
			name:        "no activity baseline",
			status:      RuntimeStatus{State: StateRunning},
			activityErr: fmt.Errorf("no attachment: %w", ErrNotFound),
			wantReason:  sleepReasonNoBaseline,
		},
		{
			name:       "recent activity is not idle",
			status:     RuntimeStatus{State: StateRunning},
			activity:   recent,
			wantReason: sleepReasonNotIdle,
		},
		{
			name:        "read failure is retryable",
			status:      RuntimeStatus{State: StateRunning},
			activityErr: errors.New("map read failed"),
			wantErr:     true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			monitor := &fakeNetworkActivityMonitor{activity: testCase.activity, err: testCase.activityErr}
			manager := buildSleepManager(t, monitor)
			desired := sleepDesired()
			if testCase.desired != nil {
				desired = testCase.desired(desired)
			}
			observed := sleepObserved()
			if testCase.observed != nil {
				observed = testCase.observed(observed)
			}

			result, err := manager.evaluateSleepEligibility(context.Background(), desired, observed, testCase.status, now)
			if testCase.wantErr != (err != nil) {
				t.Fatalf("error = %v, want error %t", err, testCase.wantErr)
			}
			if testCase.wantErr {
				return
			}
			if result.Eligible != testCase.wantEligible {
				t.Errorf("eligible = %t, want %t", result.Eligible, testCase.wantEligible)
			}
			if result.Reason != testCase.wantReason {
				t.Errorf("reason = %q, want %q", result.Reason, testCase.wantReason)
			}
		})
	}
}

func TestSleepEligibilityTimeoutBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timeout := 30 * time.Minute
	cases := []struct {
		name         string
		age          time.Duration
		wantEligible bool
	}{
		{name: "just before the timeout", age: timeout - time.Nanosecond, wantEligible: false},
		{name: "at the timeout", age: timeout, wantEligible: true},
		{name: "just after the timeout", age: timeout + time.Nanosecond, wantEligible: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			monitor := &fakeNetworkActivityMonitor{
				activity: NetworkActivity{LastSeenAt: now.Add(-testCase.age), HasBeenSeen: true},
			}
			manager := buildSleepManager(t, monitor)
			result, err := manager.evaluateSleepEligibility(context.Background(), sleepDesired(), sleepObserved(), RuntimeStatus{State: StateRunning}, now)
			if err != nil {
				t.Fatal(err)
			}
			if result.Eligible != testCase.wantEligible {
				t.Fatalf("eligible = %t, want %t", result.Eligible, testCase.wantEligible)
			}
		})
	}
}

func TestSleepEligibilityUsesOneMetalWideTimeout(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timeout := 30 * time.Minute
	monitor := &fakeNetworkActivityMonitor{}
	manager := buildSleepManager(t, monitor)

	// One VM is idle and the other has recent traffic.
	first := sleepDesired()
	monitor.activity = NetworkActivity{LastSeenAt: now.Add(-2 * timeout), HasBeenSeen: true}
	firstResult, err := manager.evaluateSleepEligibility(context.Background(), first, sleepObserved(), RuntimeStatus{State: StateRunning}, now)
	if err != nil || !firstResult.Eligible {
		t.Fatalf("first result = %+v, err = %v", firstResult, err)
	}

	second := sleepDesired()
	second.ID = "machine-2"
	second.UserID = 1001
	monitor.activity = NetworkActivity{LastSeenAt: now.Add(-time.Minute), HasBeenSeen: true}
	secondResult, err := manager.evaluateSleepEligibility(context.Background(), second, sleepObserved(), RuntimeStatus{State: StateRunning}, now)
	if err != nil || secondResult.Eligible {
		t.Fatalf("second result = %+v, err = %v", secondResult, err)
	}
}

func TestSleepPolicyRoundTripsInTheDesiredRecord(t *testing.T) {
	// The policy is per VM, so both fields must survive a record round trip.
	desired, err := json.Marshal(DesiredRecord{Sleep: SleepPolicy{IsSleepy: true, IdleTimeoutSeconds: 1800}})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := json.Marshal(ObservedRecord{Sleep: &SleepProgress{MemorySnapshotGeneration: 1, MemorySnapshotCreatedAt: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(desired), `"idle_timeout_seconds":1800`) {
		t.Fatalf("desired record lost the idle timeout: %s", desired)
	}
	// The observed record tracks progress only. It must carry no policy.
	if strings.Contains(string(observed), "idle_timeout_seconds") {
		t.Fatalf("observed record stores a policy: %s", observed)
	}
}
