package reconciler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingDriver struct {
	ids     []string
	listErr error

	mu   sync.Mutex
	seen map[string]int
	done chan string
}

func newRecordingDriver(ids ...string) *recordingDriver {
	return &recordingDriver{ids: ids, seen: map[string]int{}, done: make(chan string, 16)}
}

func (driver *recordingDriver) ActiveTargetVirtualMachineIDs(context.Context) ([]string, error) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.ids, driver.listErr
}

func (driver *recordingDriver) AdvanceTarget(_ context.Context, virtualMachineID string) error {
	driver.mu.Lock()
	driver.seen[virtualMachineID]++
	driver.mu.Unlock()
	driver.done <- virtualMachineID
	return nil
}

func TestRunAdvancesEveryActiveMigration(t *testing.T) {
	driver := newRecordingDriver("vm-1", "vm-2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go NewMigrationReconciler(driver, time.Hour, MigrationConfig{}).Run(ctx)

	got := map[string]bool{}
	for range driver.ids {
		select {
		case id := <-driver.done:
			got[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the start pass")
		}
	}
	if !got["vm-1"] || !got["vm-2"] {
		t.Fatalf("advanced = %v, want vm-1 and vm-2", got)
	}
}

func TestWakeRequestsAMigrationPass(t *testing.T) {
	driver := newRecordingDriver("vm-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconciler := NewMigrationReconciler(driver, time.Hour, MigrationConfig{})
	go reconciler.Run(ctx)

	<-driver.done
	reconciler.Wake()
	select {
	case <-driver.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wake did not trigger a pass")
	}
}

func TestMigrationRunContinuesAfterListError(t *testing.T) {
	driver := newRecordingDriver("vm-1")
	driver.listErr = errors.New("list failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconciler := NewMigrationReconciler(driver, time.Hour, MigrationConfig{})
	go reconciler.Run(ctx)

	driver.mu.Lock()
	driver.listErr = nil
	driver.mu.Unlock()
	reconciler.Wake()
	select {
	case <-driver.done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop stopped after a list error")
	}
}
