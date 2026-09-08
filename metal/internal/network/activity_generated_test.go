package network

import (
	"testing"
	"unsafe"

	"github.com/cilium/ebpf"
)

// TestActivitySpecMatchesSource confirms the embedded object still matches the C
// source. It fails when bpf2go output is stale for the map, program, or the
// rewritable user ID constant.
func TestActivitySpecMatchesSource(t *testing.T) {
	spec, err := loadActivity()
	if err != nil {
		t.Fatalf("load activity spec: %v", err)
	}

	activityMap := spec.Maps["activity_by_user_id"]
	if activityMap == nil {
		t.Fatal("map activity_by_user_id is missing")
	}
	if activityMap.Type != ebpf.Hash {
		t.Errorf("map type = %v, want Hash", activityMap.Type)
	}
	if activityMap.KeySize != 4 {
		t.Errorf("map key size = %d, want 4", activityMap.KeySize)
	}
	if activityMap.ValueSize != 8 {
		t.Errorf("map value size = %d, want 8", activityMap.ValueSize)
	}

	wakeState := spec.Maps["wake_state_by_user_id"]
	if wakeState == nil {
		t.Fatal("map wake_state_by_user_id is missing")
	}
	if wakeState.Type != ebpf.Hash {
		t.Errorf("wake state map type = %v, want Hash", wakeState.Type)
	}
	if wakeState.KeySize != 4 {
		t.Errorf("wake state map key size = %d, want 4", wakeState.KeySize)
	}
	if wakeState.ValueSize != 4 {
		t.Errorf("wake state map value size = %d, want 4", wakeState.ValueSize)
	}

	wakeEvents := spec.Maps["wake_events"]
	if wakeEvents == nil {
		t.Fatal("map wake_events is missing")
	}
	if wakeEvents.Type != ebpf.RingBuf {
		t.Errorf("wake events map type = %v, want RingBuf", wakeEvents.Type)
	}

	program := spec.Programs["record_activity"]
	if program == nil {
		t.Fatal("program record_activity is missing")
	}
	if program.Type != ebpf.SchedCLS {
		t.Errorf("program type = %v, want SchedCLS", program.Type)
	}

	if spec.Variables["virtual_machine_user_id"] == nil {
		t.Error("variable virtual_machine_user_id is missing")
	}
}

// TestWakeEventLayout confirms the generated wake event matches the C struct, so
// the ring reader decodes the user ID and packet time at the right offsets.
func TestWakeEventLayout(t *testing.T) {
	if size := unsafe.Sizeof(activityWakeEvent{}); size != 16 {
		t.Errorf("wake event size = %d, want 16", size)
	}
	if offset := unsafe.Offsetof(activityWakeEvent{}.UserId); offset != 0 {
		t.Errorf("user id offset = %d, want 0", offset)
	}
	if offset := unsafe.Offsetof(activityWakeEvent{}.PacketTime); offset != 8 {
		t.Errorf("packet time offset = %d, want 8", offset)
	}
}
