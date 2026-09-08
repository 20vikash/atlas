package vm

import (
	"context"
	"errors"
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
			NetworkWakeMonitor:     &fakeNetworkWakeMonitor{},
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

func TestSleepAbortsWhenTrafficArrives(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: false}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	// The idle check and the sample before the warm stop see idle activity. The
	// sample after the warm stop sees a newer packet, so the sleep aborts.
	idle := NetworkActivity{LastSeenAt: time.Now().Add(-time.Hour).UTC(), HasBeenSeen: true}
	advanced := NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: true}
	monitor.sequence = []NetworkActivity{idle, idle, advanced}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	if runtime.stopMode != StopWithSleepSnapshot {
		t.Fatalf("stop mode = %d, want a warm stop attempt", runtime.stopMode)
	}
	if runtime.startMode != StartFromSleepSnapshot {
		t.Fatalf("start mode = %d, want a snapshot restore on abort", runtime.startMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed after abort = %+v, want running with no sleep", observed)
	}
}

func TestSleepAbortsBeforeSnapshotWhenTrafficArrives(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	wake := &fakeNetworkWakeMonitor{}
	manager.networkWakeMonitor = wake
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}

	// A fresh attachment keeps the VM running on the first pass.
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: false}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	// The idle decision sees old activity, but the final check after arming sees a
	// newer packet. The sleep aborts before any warm stop.
	idle := NetworkActivity{LastSeenAt: time.Now().Add(-time.Hour).UTC(), HasBeenSeen: true}
	advanced := NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: true}
	monitor.sequence = []NetworkActivity{idle, advanced}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	if runtime.stops != 0 {
		t.Fatalf("stops = %d, want no warm stop before the snapshot", runtime.stops)
	}
	if len(wake.armed) == 0 || len(wake.disarmed) == 0 {
		t.Fatalf("wake arm/disarm = %d/%d, want an arm then a disarm", len(wake.armed), len(wake.disarmed))
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want running with no sleep", observed)
	}
}

func TestHeldSleepingVMRearmsWake(t *testing.T) {
	manager, _, monitor := newSleepyManager(t, 30*time.Minute)
	wake := &fakeNetworkWakeMonitor{}
	manager.networkWakeMonitor = wake
	autoSleepToSleeping(t, manager, monitor)

	armsAfterSleep := len(wake.armed)
	// A later hold pass rearms the VM, so a failed wake is armed again.
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if len(wake.armed) <= armsAfterSleep {
		t.Fatalf("armed = %d, want a rearm above %d", len(wake.armed), armsAfterSleep)
	}
}

// autoSleepToSleeping creates a sleepy VM, boots it, and lets it auto-sleep by
// reporting idle activity.
func autoSleepToSleeping(t *testing.T, manager *Manager, monitor *fakeNetworkActivityMonitor) {
	t.Helper()
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().Add(-time.Hour).UTC(), HasBeenSeen: true}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping {
		t.Fatalf("setup did not reach sleeping: %+v", observed)
	}
}

func TestRestartWhileSleepingColdBoots(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	autoSleepToSleeping(t, manager, monitor)

	if err := manager.RequestRestart(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.discards != 1 || runtime.startMode != StartNormal {
		t.Fatalf("discards = %d start mode = %d, want a discard and a cold boot", runtime.discards, runtime.startMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	desired, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want running with no sleep", observed)
	}
	if observed.RestartGeneration != desired.RestartGeneration {
		t.Fatalf("restart generation = %d, want %d", observed.RestartGeneration, desired.RestartGeneration)
	}
}

func TestDiskChangeWhileSleepingColdBoots(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	autoSleepToSleeping(t, manager, monitor)

	// A disk grow changes the specification shape, so the snapshot is incompatible.
	if err := manager.SetDisk(context.Background(), "machine-1", 8192, Disk{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.discards != 1 || runtime.startMode != StartNormal {
		t.Fatalf("discards = %d start mode = %d, want a discard and a cold boot", runtime.discards, runtime.startMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want running with no sleep", observed)
	}
}

func TestDisableSleepyWhileSleepingResumes(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	autoSleepToSleeping(t, manager, monitor)

	// Disabling the policy keeps the shape, so the compatible snapshot resumes
	// instead of cold booting.
	if err := manager.SetSleepPolicy(context.Background(), "machine-1", false); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.startMode != StartFromSleepSnapshot || runtime.discards != 0 {
		t.Fatalf("start mode = %d discards = %d, want a snapshot resume", runtime.startMode, runtime.discards)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want running with no sleep", observed)
	}
}

func TestSetComputeRejectedWhileSleeping(t *testing.T) {
	manager, _, monitor := newSleepyManager(t, 30*time.Minute)
	autoSleepToSleeping(t, manager, monitor)

	// A compute change needs a stopped VM, so it is rejected while sleeping.
	if err := manager.SetCompute(context.Background(), "machine-1", 4, 4096); !errors.Is(err, ErrConflict) {
		t.Fatalf("SetCompute error = %v, want conflict", err)
	}
}

func TestDestroyWhileSleepingRemovesArtifacts(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	autoSleepToSleeping(t, manager, monitor)

	// A destroy removes the runtime, which drops the sleep snapshot, then the
	// network and storage, and finally the records.
	if err := manager.Delete(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.removes != 1 {
		t.Fatalf("removes = %d, want the runtime removed once", runtime.removes)
	}
	if _, err := manager.store.readDesired("machine-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("records remain after destroy: %v", err)
	}
}

// warmStopToSleeping creates a VM, boots it, and warm-stops it to the sleeping
// state through the manual power path.
func warmStopToSleeping(t *testing.T, manager *Manager) {
	t.Helper()
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping {
		t.Fatalf("setup did not reach sleeping: %+v", observed)
	}
}

func TestPausedWhileSleepingLoadsPaused(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	warmStopToSleeping(t, manager)

	if err := manager.SetPowerState(context.Background(), "machine-1", StatePaused); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.startMode != StartFromSleepSnapshotPaused {
		t.Fatalf("start mode = %d, want a paused snapshot load", runtime.startMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StatePaused || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want paused with no sleep", observed)
	}
}

func TestPlainStopWhileSleepingDiscards(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	warmStopToSleeping(t, manager)

	if err := manager.SetPowerState(context.Background(), "machine-1", StateStopped); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.discards != 1 {
		t.Fatalf("discards = %d, want the snapshot discarded once", runtime.discards)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateStopped || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want stopped with no sleep", observed)
	}
}

// seedInterruptedSleep drives a running sleepy VM, then rewrites its observed
// record to model a crash inside the sleep-snapshot phase: the process is gone
// but the sleeping status was never written.
func seedInterruptedSleep(t *testing.T, manager *Manager, runtime *fakeRuntime, monitor *fakeNetworkActivityMonitor) {
	t.Helper()
	if _, err := manager.Create(context.Background(), "machine-1", sleepySpecification()); err != nil {
		t.Fatal(err)
	}
	monitor.activity = NetworkActivity{LastSeenAt: time.Now().UTC(), HasBeenSeen: false}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	observed.Phase = phaseSleepSnapshot
	observed.Sleep = &SleepProgress{RequestedAt: time.Now().UTC()}
	if err := manager.store.writeObserved("machine-1", observed); err != nil {
		t.Fatal(err)
	}
	runtime.state = StateStopped
}

func TestRecoverInterruptedSleepPublishesSleeping(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	seedInterruptedSleep(t, manager, runtime, monitor)
	runtime.sleepSnapshot = SleepSnapshot{Generation: 4, CreatedAt: time.Unix(2000, 0).UTC()}

	// A daemon restart recovers the sleeping status from the valid snapshot.
	recreated, err := NewManager(manager.configuration, ManagerDependencies{
		Runtime:                runtime,
		Network:                &fakeNetwork{},
		Storage:                &fakeStorage{},
		Snapshots:              fakeSnapshots{},
		NetworkActivityMonitor: monitor,
		NetworkWakeMonitor:     &fakeNetworkWakeMonitor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recreated.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.stops != 0 {
		t.Fatalf("stops = %d, recovery must not snapshot again", runtime.stops)
	}
	observed, err := recreated.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping || observed.Sleep == nil || observed.Sleep.SnapshotGeneration != 4 {
		t.Fatalf("observed = %+v, want sleeping from generation 4", observed)
	}
}

func TestRecoverInterruptedSleepReportsInvalidSnapshot(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	seedInterruptedSleep(t, manager, runtime, monitor)
	runtime.sleepSnapshotError = errors.New("corrupt manifest")

	// An invalid snapshot is a failure. The VM must not cold boot.
	if err := manager.Reconcile(context.Background(), "machine-1"); err == nil {
		t.Fatal("expected an invalid snapshot to surface")
	}
	if runtime.startMode == StartNormal && runtime.starts > 1 {
		t.Fatal("an invalid snapshot must not cold boot")
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State == StateSleeping || observed.Error == nil {
		t.Fatalf("observed = %+v, want a recorded failure and no sleeping state", observed)
	}
}

func TestRecoverInterruptedSleepWithoutSnapshotClears(t *testing.T) {
	manager, runtime, monitor := newSleepyManager(t, 30*time.Minute)
	seedInterruptedSleep(t, manager, runtime, monitor)
	runtime.sleepSnapshotError = ErrNotFound

	// No snapshot published, so the sleep intent clears and nothing stays pending.
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State == StateSleeping || observed.Sleep != nil || observed.Phase == phaseSleepSnapshot {
		t.Fatalf("observed = %+v, want a cleared sleep intent", observed)
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
