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
	steps := defaultRouteSteps("10.0.0.1")

	if !slices.Contains(steps[0], "replace") || slices.Contains(steps[0], "add") {
		t.Fatalf("default route step = %v, want replace", steps[0])
	}
}
