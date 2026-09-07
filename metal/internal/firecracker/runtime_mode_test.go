package firecracker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/vm"
)

// The dispatch for an unknown mode returns before touching the machine, so a zero
// Runtime is enough to test it.
func TestRuntimeStartRejectsAnUnknownMode(t *testing.T) {
	runtime := &Runtime{}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}

	err := runtime.Start(context.Background(), input, vm.StartMode(99))
	if err == nil || !strings.Contains(err.Error(), "unknown start mode") {
		t.Errorf("Start(unknown mode) error = %v, want an unknown start mode error", err)
	}
}

// The snapshot restore modes require a snapshot. With none published they report
// the not-found result rather than a cold boot.
func TestRuntimeStartFromSleepSnapshotWithoutASnapshot(t *testing.T) {
	runtime := &Runtime{configuration: Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}

	for _, mode := range []vm.StartMode{vm.StartFromSleepSnapshot, vm.StartFromSleepSnapshotPaused} {
		if err := runtime.Start(context.Background(), input, mode); !errors.Is(err, errSnapshotNotFound) {
			t.Fatalf("Start(mode %d) error = %v, want errSnapshotNotFound", mode, err)
		}
	}
}

func TestRuntimeStopRejectsAnUnknownMode(t *testing.T) {
	runtime := &Runtime{}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}
	err := runtime.Stop(context.Background(), input, vm.StopMode(99))
	if err == nil || !strings.Contains(err.Error(), "unknown stop mode") {
		t.Errorf("Stop(unknown mode) error = %v, want an unknown stop mode error", err)
	}
}
