package vm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeNetworkWakeMonitor records arm and disarm calls for wake tests.
type fakeNetworkWakeMonitor struct {
	armed     []NetworkActivityRequest
	disarmed  []NetworkActivityRequest
	events    chan NetworkWakeEvent
	armError  error
	disarmErr error
}

func (monitor *fakeNetworkWakeMonitor) ArmNetworkWake(request NetworkActivityRequest) error {
	monitor.armed = append(monitor.armed, request)
	return monitor.armError
}

func (monitor *fakeNetworkWakeMonitor) DisarmNetworkWake(request NetworkActivityRequest) error {
	monitor.disarmed = append(monitor.disarmed, request)
	return monitor.disarmErr
}

func (monitor *fakeNetworkWakeMonitor) NetworkWakeEvents() <-chan NetworkWakeEvent {
	return monitor.events
}

// sleepingWakeManager builds a sleeping VM with a wake monitor.
func sleepingWakeManager(t *testing.T) (*Manager, *fakeRuntime, *fakeNetworkWakeMonitor, uint32) {
	t.Helper()
	manager, runtime, activity := newSleepyManager(t, 30*time.Minute)
	wakeMonitor := &fakeNetworkWakeMonitor{}
	manager.networkWakeMonitor = wakeMonitor
	autoSleepToSleeping(t, manager, activity)

	desired, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	// Reset counters so tests see only wake calls.
	runtime.starts = 0
	runtime.startMode = StartNormal
	return manager, runtime, wakeMonitor, desired.UserID
}

func TestWakeFromNetworkRestoresRunning(t *testing.T) {
	manager, runtime, wakeMonitor, userID := sleepingWakeManager(t)

	event := NetworkWakeEvent{VirtualMachineID: "machine-1", UserID: userID, PacketTime: 123}
	if err := manager.WakeFromNetwork(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	if runtime.startMode != StartFromSleepSnapshot {
		t.Fatalf("start mode = %d, want StartFromSleepSnapshot", runtime.startMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed = %+v, want running with no sleep", observed)
	}
	if len(wakeMonitor.disarmed) != 1 || wakeMonitor.disarmed[0].UserID != userID {
		t.Fatalf("disarm calls = %+v, want one for user %d", wakeMonitor.disarmed, userID)
	}
}

func TestWakeFromNetworkWrongUserIDIsANoOp(t *testing.T) {
	manager, runtime, wakeMonitor, userID := sleepingWakeManager(t)

	event := NetworkWakeEvent{VirtualMachineID: "machine-1", UserID: userID + 1, PacketTime: 1}
	if err := manager.WakeFromNetwork(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	if runtime.startMode != StartNormal {
		t.Fatalf("start mode = %d, want no restore for a wrong user ID", runtime.startMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping {
		t.Fatalf("observed = %+v, want still sleeping", observed)
	}
	if len(wakeMonitor.disarmed) != 0 {
		t.Fatalf("disarm calls = %+v, want none", wakeMonitor.disarmed)
	}
}

func TestWakeFromNetworkDuplicateEventIsANoOp(t *testing.T) {
	manager, runtime, _, userID := sleepingWakeManager(t)
	event := NetworkWakeEvent{VirtualMachineID: "machine-1", UserID: userID, PacketTime: 1}
	if err := manager.WakeFromNetwork(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	// The VM is running now. A duplicate event must not restore again.
	runtime.startMode = StartNormal
	if err := manager.WakeFromNetwork(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if runtime.startMode != StartNormal {
		t.Fatalf("a duplicate event restored again: start mode = %d", runtime.startMode)
	}
}

func TestWakeFromNetworkWrongDesiredStateIsANoOp(t *testing.T) {
	manager, runtime, _, userID := sleepingWakeManager(t)

	// The controller asked for stopped, so a wake must not restore a running VM.
	desired, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	desired.State = StateStopped
	if err := manager.store.writeDesired(desired); err != nil {
		t.Fatal(err)
	}

	event := NetworkWakeEvent{VirtualMachineID: "machine-1", UserID: userID, PacketTime: 1}
	if err := manager.WakeFromNetwork(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if runtime.startMode != StartNormal {
		t.Fatalf("a wake restored despite a desired stop: start mode = %d", runtime.startMode)
	}
}

func TestWakeFromNetworkRestoreErrorKeepsTheSnapshot(t *testing.T) {
	manager, runtime, wakeMonitor, userID := sleepingWakeManager(t)
	runtime.startFromMemorySnapshotError = errors.New("load snapshot failed")

	event := NetworkWakeEvent{VirtualMachineID: "machine-1", UserID: userID, PacketTime: 1}
	if err := manager.WakeFromNetwork(context.Background(), event); err == nil {
		t.Fatal("want a restore error")
	}

	if runtime.startMode != StartFromSleepSnapshot {
		t.Fatalf("start mode = %d, want the snapshot restore attempt", runtime.startMode)
	}
	if runtime.starts != 0 {
		t.Fatalf("starts = %d, a failed restore must not cold boot", runtime.starts)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping || observed.Sleep == nil {
		t.Fatalf("observed = %+v, want still sleeping with the snapshot kept", observed)
	}
	if len(wakeMonitor.disarmed) != 0 {
		t.Fatalf("disarm calls = %+v, want none after a failed restore", wakeMonitor.disarmed)
	}
}
