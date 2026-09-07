package firecracker

import (
	"context"
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
		vm.StartFromSleepSnapshot:       "not implemented",
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

func TestRuntimeStopRejectsAnUnknownMode(t *testing.T) {
	runtime := &Runtime{}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}
	err := runtime.Stop(context.Background(), input, vm.StopMode(99))
	if err == nil || !strings.Contains(err.Error(), "unknown stop mode") {
		t.Errorf("Stop(unknown mode) error = %v, want an unknown stop mode error", err)
	}
}
