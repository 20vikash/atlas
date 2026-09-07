package vm

import (
	"context"
	"testing"
	"time"
)

// newSleepyManager builds a manager with automatic sleep enabled and a
// controllable activity monitor. It returns the runtime and monitor so a test
// can drive warm stops and activity.
func newSleepyManager(t *testing.T, timeout time.Duration) (*Manager, *fakeRuntime, *fakeNetworkActivityMonitor) {
	t.Helper()
	runtime := &fakeRuntime{
		state:       StateStopped,
		stopOutcome: StopOutcome{SnapshotGeneration: 1, SnapshotCreatedAt: time.Unix(1000, 0).UTC()},
	}
	monitor := &fakeNetworkActivityMonitor{}
	manager, err := NewManager(
		ManagerConfig{
			MachinesDirectory: t.TempDir(),
			UserIDRange:       UserIDRange{Min: 1000, Max: 1010},
			Sleep:             SleepConfig{Enabled: true, IdleTimeout: timeout},
		},
		ManagerDependencies{
			Runtime:                runtime,
			Network:                &fakeNetwork{},
			Storage:                &fakeStorage{},
			Snapshots:              fakeSnapshots{},
			NetworkActivityMonitor: monitor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager, runtime, monitor
}

func sleepySpecification() Specification {
	specification := testSpecification()
	specification.IsSleepy = true
	return specification
}

func TestIdleSleepyVMReachesSleeping(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}

	// A fresh attachment reports its baseline, so the first pass keeps the VM
	// running instead of sleeping a VM that has seen no packet.
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: false}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || runtime.stops != 0 {
		t.Fatalf("recent activity slept the VM: %+v stops=%d", observed, runtime.stops)
	}

	// Activity older than the timeout makes the VM eligible for sleep.
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().Add(-time.Hour).UTC(), HasBeenSeen: true}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.stopMode != StopWithSleepSnapshot {
		t.Fatalf("stop mode = %d, want StopWithSleepSnapshot", runtime.stopMode)
	}
	observed, err = manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping || observed.Sleep == nil {
		t.Fatalf("observed = %+v, want sleeping with progress", observed)
	}
	if observed.Sleep.SnapshotGeneration != 1 || observed.Sleep.LastNetworkActivityAt.IsZero() {
		t.Fatalf("sleep progress = %+v", observed.Sleep)
	}
	desired, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if desired.State != StateRunning {
		t.Fatal("desired state must stay running while asleep")
	}
}

func TestAutomaticSleepStaysAsleep(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().Add(-time.Hour).UTC(), HasBeenSeen: true}
	for range 3 {
		if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
			t.Fatal(err)
		}
	}

	// The VM sleeps once and later passes hold it. They do not snapshot again.
	if runtime.stops != 1 {
		t.Fatalf("stops = %d, want one warm stop", runtime.stops)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping {
		t.Fatalf("observed = %+v, want sleeping", observed)
	}
}

func TestActiveSleepyVMStaysRunning(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: true}
	for range 3 {
		if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.stops != 0 {
		t.Fatalf("stops = %d, want none for an active VM", runtime.stops)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning {
		t.Fatalf("observed = %+v, want running", observed)
	}
}
