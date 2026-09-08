package vm

import "testing"

func TestSleepingIsAnObservedStateOnly(t *testing.T) {
	if !isObservedState(StateSleeping) {
		t.Error("sleeping must be a valid observed state")
	}
	if IsDesiredState(StateSleeping) {
		t.Error("sleeping must not be a valid desired state")
	}
}

func TestSameReservationDistinguishesTheSleepyFlag(t *testing.T) {
	base := Specification{VirtualCPUCount: 1, MemoryMiB: 1}
	sleepy := base
	sleepy.IsSleepy = true

	if base.SameReservation(sleepy) {
		t.Error("specifications that differ in is_sleepy must not be the same reservation")
	}
	if !base.SameReservation(base) {
		t.Error("identical specifications must be the same reservation")
	}
}

func TestEgressCapabilities(t *testing.T) {
	for _, testCase := range []struct {
		egress          Egress
		virtualEthernet bool
		internetPath    bool
	}{
		{EgressUplink, true, true},
		{EgressMesh, true, false},
		{EgressNone, false, false},
	} {
		if got := testCase.egress.HasVirtualEthernet(); got != testCase.virtualEthernet {
			t.Fatalf("egress %q veth = %v, want %v", testCase.egress, got, testCase.virtualEthernet)
		}
		if got := testCase.egress.HasInternetPath(); got != testCase.internetPath {
			t.Fatalf("egress %q internet = %v, want %v", testCase.egress, got, testCase.internetPath)
		}
	}
}

func TestEgressIsValidRejectsUnknownModes(t *testing.T) {
	for _, egress := range []Egress{EgressUplink, EgressMesh, EgressNone} {
		if !egress.IsValid() {
			t.Fatalf("egress %q must be valid", egress)
		}
	}
	for _, egress := range []Egress{"", "host", "server"} {
		if egress.IsValid() {
			t.Fatalf("egress %q must not be valid", egress)
		}
	}
}
