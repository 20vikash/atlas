package network

import (
	"testing"

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
