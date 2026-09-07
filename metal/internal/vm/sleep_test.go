package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// buildSleepManager returns a manager with a sleep policy and a controllable
// activity monitor. Tests drive the eligibility decision through it.
func buildSleepManager(t *testing.T, config SleepConfig, monitor NetworkActivityMonitor) *Manager {
	t.Helper()
	manager, err := NewManager(
		ManagerConfig{
			MachinesDirectory: t.TempDir(),
			UserIDRange:       UserIDRange{Min: 1000, Max: 1010},
			Sleep:             config,
		},
		ManagerDependencies{
			Runtime:                &fakeRuntime{},
			Network:                &fakeNetwork{},
			Storage:                &fakeStorage{},
			Snapshots:              fakeSnapshots{},
			NetworkActivityMonitor: monitor,
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
		Specification:     Specification{IsSleepy: true},
	}
}

func TestNewManagerValidatesSleepConfiguration(t *testing.T) {
	monitor := &fakeNetworkActivityMonitor{}
	cases := []struct {
		name    string
		config  SleepConfig
		monitor NetworkActivityMonitor
		wantErr bool
	}{
		{name: "disabled needs nothing", config: SleepConfig{}, monitor: nil, wantErr: false},
		{name: "enabled needs a timeout", config: SleepConfig{Enabled: true}, monitor: monitor, wantErr: true},
		{name: "enabled needs a positive timeout", config: SleepConfig{Enabled: true, IdleTimeout: -time.Minute}, monitor: monitor, wantErr: true},
		{name: "enabled needs a monitor", config: SleepConfig{Enabled: true, IdleTimeout: time.Minute}, monitor: nil, wantErr: true},
		{name: "enabled with both is accepted", config: SleepConfig{Enabled: true, IdleTimeout: time.Minute}, monitor: monitor, wantErr: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewManager(
				ManagerConfig{MachinesDirectory: t.TempDir(), Sleep: testCase.config},
				ManagerDependencies{
					Runtime:                &fakeRuntime{},
					Network:                &fakeNetwork{},
					Storage:                &fakeStorage{},
					Snapshots:              fakeSnapshots{},
					NetworkActivityMonitor: testCase.monitor,
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
	manager := buildSleepManager(t, SleepConfig{Enabled: true, IdleTimeout: time.Minute}, monitor)
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
	manager := buildSleepManager(t, SleepConfig{Enabled: true, IdleTimeout: time.Minute}, notFound)
	if _, err := manager.sampleNetworkActivity(context.Background(), sleepDesired()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}

	readFailure := &fakeNetworkActivityMonitor{err: errors.New("map read failed")}
	manager = buildSleepManager(t, SleepConfig{Enabled: true, IdleTimeout: time.Minute}, readFailure)
	_, err := manager.sampleNetworkActivity(context.Background(), sleepDesired())
	if err == nil || !strings.Contains(err.Error(), "machine-1") {
		t.Fatalf("error = %v, want a wrapped VM read failure", err)
	}
}
