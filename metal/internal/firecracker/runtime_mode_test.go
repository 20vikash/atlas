package firecracker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/vm"
)

// The dispatch for an unimplemented or unknown mode returns before touching the
// machine, so a zero Runtime is enough to test it.
func TestRuntimeStartRejectsUnimplementedAndUnknownModes(t *testing.T) {
	runtime := &Runtime{}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}
	cases := map[vm.StartMode]string{
		vm.StartFromSleepSnapshotPaused: "not implemented",
		vm.StartMode(99):                "unknown start mode",
	}
	for mode, want := range cases {
		err := runtime.Start(context.Background(), input, mode)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Start(mode %d) error = %v, want %q", mode, err, want)
		}
	}
}

// StartFromSleepSnapshot requires a snapshot. With none published it reports the
// not-found result rather than a cold boot.
func TestRuntimeStartFromSleepSnapshotWithoutASnapshot(t *testing.T) {
	runtime := &Runtime{configuration: Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}

	if err := runtime.Start(context.Background(), input, vm.StartFromSleepSnapshot); !errors.Is(err, errSnapshotNotFound) {
		t.Fatalf("error = %v, want errSnapshotNotFound", err)
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
