package vm

import "testing"

func TestSleepingIsNotAVirtualMachineState(t *testing.T) {
	if isObservedState(State("sleeping")) || IsDesiredState(State("sleeping")) {
		t.Error("sleeping must not be a virtual machine state")
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

func TestVirtualCPUCountRoundsMillicoresUp(t *testing.T) {
	for _, testCase := range []struct {
		cpuMillicores int
		want          int
	}{
		{100, 1},
		{999, 1},
		{1000, 1},
		{1001, 2},
		{32000, 32},
	} {
		specification := Specification{CPUMillicores: testCase.cpuMillicores}
		if got := specification.VirtualCPUCount(); got != testCase.want {
			t.Errorf("VirtualCPUCount(%d) = %d, want %d", testCase.cpuMillicores, got, testCase.want)
		}
	}
}

func TestFirewallChangesTheReservation(t *testing.T) {
	first := Specification{Network: NetworkConfiguration{Firewall: FirewallConfiguration{Enabled: false}}}
	second := first
	second.Network.Firewall.Enabled = true

	if first.SameReservation(second) {
		t.Fatal("different firewalls have the same reservation")
	}
}

func TestCloneSpecificationCopiesFirewallRules(t *testing.T) {
	original := Specification{
		Network: NetworkConfiguration{
			Firewall: FirewallConfiguration{
				Inbound: []FirewallRule{{
					Protocol: FirewallProtocolTCP,
					Ports:    "22",
					CIDRs:    []string{"203.0.113.0/24"},
				}},
			},
		},
	}
	cloned := cloneSpecification(original)
	cloned.Network.Firewall.Inbound[0].CIDRs[0] = "198.51.100.0/24"
	cloned.Network.Firewall.Inbound[0].Ports = "443"

	if original.Network.Firewall.Inbound[0].CIDRs[0] != "203.0.113.0/24" {
		t.Fatal("clone shares firewall CIDRs with the source")
	}
	if original.Network.Firewall.Inbound[0].Ports != "22" {
		t.Fatal("clone shares firewall rules with the source")
	}
}
