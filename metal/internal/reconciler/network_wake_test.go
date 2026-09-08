package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

// fakeNetworkWakeManager records wake calls and can fail one restore.
type fakeNetworkWakeManager struct {
	calls       chan vm.NetworkWakeEvent
	hasDeadline chan bool
	failFirst   bool
	failedOnce  bool
	returnErr   error
}

func (manager *fakeNetworkWakeManager) WakeFromNetwork(ctx context.Context, event vm.NetworkWakeEvent) error {
	if manager.hasDeadline != nil {
		_, ok := ctx.Deadline()
		manager.hasDeadline <- ok
	}
	if manager.calls != nil {
		manager.calls <- event
	}
	if manager.failFirst && !manager.failedOnce {
		manager.failedOnce = true
		return errors.New("restore failed")
	}
	return manager.returnErr
}

// runReconciler starts the reconciler and reports when it returns.
func runReconciler(ctx context.Context, r *NetworkWakeReconciler) chan struct{} {
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r.Run(ctx)
	}()
	return finished
}

func TestNetworkWakeReconcilerProcessesEvents(t *testing.T) {
	events := make(chan vm.NetworkWakeEvent)
	manager := &fakeNetworkWakeManager{calls: make(chan vm.NetworkWakeEvent, 1), hasDeadline: make(chan bool, 1)}
	reconciler := NewNetworkWakeReconciler(manager, events, NetworkWakeConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	finished := runReconciler(ctx, reconciler)

	events <- vm.NetworkWakeEvent{VirtualMachineID: "vm-1", UserID: 100001}
	if hasDeadline := <-manager.hasDeadline; !hasDeadline {
		t.Error("the wake operation must run under a deadline")
	}
	if event := <-manager.calls; event.VirtualMachineID != "vm-1" {
		t.Fatalf("event = %+v, want vm-1", event)
	}

	cancel()
	waitClosed(t, finished)
}

func TestNetworkWakeReconcilerContinuesAfterAManagerError(t *testing.T) {
	events := make(chan vm.NetworkWakeEvent)
	manager := &fakeNetworkWakeManager{calls: make(chan vm.NetworkWakeEvent, 2), failFirst: true}
	reconciler := NewNetworkWakeReconciler(manager, events, NetworkWakeConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	finished := runReconciler(ctx, reconciler)

	// A failed first wake must not stop the loop from serving the next event.
	events <- vm.NetworkWakeEvent{VirtualMachineID: "vm-1", UserID: 1}
	events <- vm.NetworkWakeEvent{VirtualMachineID: "vm-2", UserID: 2}
	first := <-manager.calls
	second := <-manager.calls
	if first.VirtualMachineID != "vm-1" || second.VirtualMachineID != "vm-2" {
		t.Fatalf("events = %s, %s, want vm-1, vm-2", first.VirtualMachineID, second.VirtualMachineID)
	}

	cancel()
	waitClosed(t, finished)
}

func TestNetworkWakeReconcilerStopsOnAClosedChannel(t *testing.T) {
	events := make(chan vm.NetworkWakeEvent)
	reconciler := NewNetworkWakeReconciler(&fakeNetworkWakeManager{}, events, NetworkWakeConfig{})

	finished := runReconciler(context.Background(), reconciler)
	close(events)
	waitClosed(t, finished)
}

func TestNetworkWakeReconcilerStopsOnCancel(t *testing.T) {
	events := make(chan vm.NetworkWakeEvent)
	reconciler := NewNetworkWakeReconciler(&fakeNetworkWakeManager{}, events, NetworkWakeConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	finished := runReconciler(ctx, reconciler)
	cancel()
	waitClosed(t, finished)
}

// waitClosed fails if the channel does not close within a short window.
func waitClosed(t *testing.T, finished chan struct{}) {
	t.Helper()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the reconciler did not stop")
	}
}
