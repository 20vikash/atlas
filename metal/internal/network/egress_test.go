package network

import (
	"slices"
	"testing"
)

func TestRuleCommentMatchesExactly(t *testing.T) {
	arguments := []string{"-A", "PREROUTING", "--comment", "metal-public-ipv4-vm-10"}

	if !hasRuleComment(arguments, "metal-public-ipv4-vm-10") {
		t.Fatal("exact comment did not match")
	}
	if hasRuleComment(arguments, "metal-public-ipv4-vm-1") {
		t.Fatal("comment prefix matched another VM")
	}
}

func TestPublicIPv4RuleCheckRemovesInsertPosition(t *testing.T) {
	step := []string{"iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", "10.0.0.2", "-j", "SNAT"}
	want := []string{"iptables", "-t", "nat", "-C", "POSTROUTING", "-s", "10.0.0.2", "-j", "SNAT"}

	if check := publicIPv4RuleCheck(step); !slices.Equal(check, want) {
		t.Fatalf("rule check = %v, want %v", check, want)
	}
}

func TestDefaultRouteStepsAreSafeToRepeat(t *testing.T) {
	steps := defaultRouteSteps("metal-vm-1", "10.0.0.1")

	if !slices.Contains(steps[0], "replace") || slices.Contains(steps[0], "add") {
		t.Fatalf("default route step = %v, want replace", steps[0])
	}
}

func TestInternetPathStepsExtendTheDefaultRoute(t *testing.T) {
	route := defaultRouteSteps("metal-vm-1", "10.0.0.1")
	steps := internetPathSteps("metal-vm-1", "vg-1000", "10.0.0.1")

	if len(steps) != len(route)+1 {
		t.Fatalf("internet path steps = %d, want %d", len(steps), len(route)+1)
	}
	for index, step := range route {
		if !slices.Equal(steps[index], step) {
			t.Fatalf("step %d = %v, want %v", index, step, route[index])
		}
	}
	if !slices.Contains(steps[len(steps)-1], "MASQUERADE") {
		t.Fatalf("last step = %v, want MASQUERADE", steps[len(steps)-1])
	}
}
