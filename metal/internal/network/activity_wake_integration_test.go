//go:build linux && integration

package network

import (
	"errors"
	"os"
	"testing"

	"github.com/cilium/ebpf"
)

// sharedHashMap creates one shared hash map from the spec with the given
// capacity. The test owns it and closes it.
func sharedHashMap(t *testing.T, spec *ebpf.CollectionSpec, name string, capacity uint32) *ebpf.Map {
	t.Helper()
	mapSpec := spec.Maps[name].Copy()
	mapSpec.MaxEntries = capacity
	handle, err := ebpf.NewMap(mapSpec)
	if err != nil {
		t.Fatalf("create shared map %s: %v", name, err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// sharedRingMap creates one shared ring buffer map from the spec. The test owns
// it and closes it.
func sharedRingMap(t *testing.T, spec *ebpf.CollectionSpec, name string) *ebpf.Map {
	t.Helper()
	handle, err := ebpf.NewMap(spec.Maps[name].Copy())
	if err != nil {
		t.Fatalf("create shared ring map %s: %v", name, err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// loadWakeProgram loads the activity program with its three maps replaced by the
// given shared maps. It rewrites the user ID constant, like the real loader.
func loadWakeProgram(t *testing.T, spec *ebpf.CollectionSpec, userID uint32, activity, wakeState, wakeEvents *ebpf.Map) *ebpf.Program {
	t.Helper()
	if err := spec.Variables["virtual_machine_user_id"].Set(userID); err != nil {
		t.Fatalf("set user id: %v", err)
	}
	// Match the small C capacities to the shared maps, so the replacement passes
	// the compatibility check.
	spec.Maps["activity_by_user_id"].MaxEntries = activity.MaxEntries()
	spec.Maps["wake_state_by_user_id"].MaxEntries = wakeState.MaxEntries()

	var programs activityPrograms
	err := spec.LoadAndAssign(&programs, &ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{
			"activity_by_user_id":   activity,
			"wake_state_by_user_id": wakeState,
			"wake_events":           wakeEvents,
		},
	})
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Fatalf("verifier rejected the wake program:\n%+v", verifier)
		}
		t.Fatalf("load wake program: %v", err)
	}
	t.Cleanup(func() { _ = programs.RecordActivity.Close() })
	return programs.RecordActivity
}

// TestWakeProgramLoadsWithSharedMaps loads the wake program into the kernel and
// replaces its three maps with shared maps. It proves the program verifies and
// the shared map definitions are compatible replacements. It needs root.
//
//	sudo -E go test -tags integration -run TestWakeProgramLoadsWithSharedMaps ./internal/network/
func TestWakeProgramLoadsWithSharedMaps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	spec, err := loadActivity()
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	const capacity = 1024
	activity := sharedHashMap(t, spec, "activity_by_user_id", capacity)
	wakeState := sharedHashMap(t, spec, "wake_state_by_user_id", capacity)
	wakeEvents := sharedRingMap(t, spec, "wake_events")

	loadWakeProgram(t, spec, 100001, activity, wakeState, wakeEvents)
}
