package reconciler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingTarget struct {
	ids     []string
	listErr error

	mu   sync.Mutex
	seen map[string]int
	done chan string
}

func newRecordingTarget(ids ...string) *recordingTarget {
	return &recordingTarget{ids: ids, seen: map[string]int{}, done: make(chan string, 16)}
}

func (target *recordingTarget) ListIDs(context.Context) ([]string, error) {
	target.mu.Lock()
	defer target.mu.Unlock()
	return target.ids, target.listErr
}

func (target *recordingTarget) Reconcile(_ context.Context, id string) error {
	target.mu.Lock()
	target.seen[id]++
	target.mu.Unlock()
	target.done <- id
	return nil
}

func TestRunReconcilesEveryVirtualMachine(t *testing.T) {
	target := newRecordingTarget("a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go New(target, time.Hour, Config{}).Run(ctx)

	got := map[string]bool{}
	for range target.ids {
		select {
		case id := <-target.done:
			got[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the start pass")
		}
	}
	if !got["a"] || !got["b"] {
		t.Fatalf("reconciled = %v, want a and b", got)
	}
}

func TestWakeRequestsReconciliation(t *testing.T) {
	target := newRecordingTarget("a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconciler := New(target, time.Hour, Config{})
	go reconciler.Run(ctx)

	<-target.done
	reconciler.Wake()
	select {
	case <-target.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wake did not trigger a pass")
	}
}

func TestRunContinuesAfterListError(t *testing.T) {
	target := newRecordingTarget("a")
	target.listErr = errors.New("list failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconciler := New(target, time.Hour, Config{})
	go reconciler.Run(ctx)

	target.mu.Lock()
	target.listErr = nil
	target.mu.Unlock()
	reconciler.Wake()
	select {
	case <-target.done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop stopped after a list error")
	}
}

type concurrencyTarget struct {
	ids     []string
	started chan string
	release chan struct{}

	mutex           sync.Mutex
	active          int
	maximumObserved int
}

func (target *concurrencyTarget) ListIDs(context.Context) ([]string, error) { return target.ids, nil }

func (target *concurrencyTarget) Reconcile(ctx context.Context, id string) error {
	target.mutex.Lock()
	target.active++
	if target.active > target.maximumObserved {
		target.maximumObserved = target.active
	}
	target.mutex.Unlock()

	target.started <- id
	select {
	case <-target.release:
	case <-ctx.Done():
	}

	target.mutex.Lock()
	target.active--
	target.mutex.Unlock()
	return ctx.Err()
}

func TestReconcileAllLimitsConcurrentOperations(t *testing.T) {
	target := &concurrencyTarget{
		ids:     []string{"a", "b", "c", "d"},
		started: make(chan string, 4),
		release: make(chan struct{}),
	}
	reconciler := New(target, time.Hour, Config{
		MaxConcurrentOperations: 2,
		OperationTimeout:        time.Second,
	})
	done := make(chan struct{})
	go func() {
		reconciler.reconcileAll(context.Background())
		close(done)
	}()

	<-target.started
	<-target.started
	select {
	case id := <-target.started:
		t.Fatalf("operation %s started above the limit", id)
	case <-time.After(20 * time.Millisecond):
	}

	for range 2 {
		target.release <- struct{}{}
	}
	<-target.started
	<-target.started
	for range 2 {
		target.release <- struct{}{}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconcile pass did not finish")
	}
	if target.maximumObserved != 2 {
		t.Fatalf("maximum concurrent operations = %d, want 2", target.maximumObserved)
	}
}

type timeoutTarget struct {
	timedOut chan struct{}
}

func (target *timeoutTarget) ListIDs(context.Context) ([]string, error) { return []string{"a"}, nil }

func (target *timeoutTarget) Reconcile(ctx context.Context, _ string) error {
	<-ctx.Done()
	close(target.timedOut)
	return ctx.Err()
}

func TestReconcileAllAppliesOperationTimeout(t *testing.T) {
	target := &timeoutTarget{timedOut: make(chan struct{})}
	reconciler := New(target, time.Hour, Config{
		MaxConcurrentOperations: 1,
		OperationTimeout:        20 * time.Millisecond,
	})

	reconciler.reconcileAll(context.Background())
	select {
	case <-target.timedOut:
	default:
		t.Fatal("reconcile operation did not time out")
	}
}
