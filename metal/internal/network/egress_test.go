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

func TestPublicIPv4StepsMapHostOriginatedTraffic(t *testing.T) {
	steps := publicIPv4Steps("vm-10", "metal-vm-10", "vpeer0", "10.6.26.130", "203.0.113.7")

	want := []string{
		"iptables", "-t", "nat", "-A", "OUTPUT", "-d", "203.0.113.7",
		"-m", "comment", "--comment", "metal-public-ipv4-vm-10",
		"-j", "DNAT", "--to-destination", "10.6.26.130",
	}
	if !slices.ContainsFunc(steps, func(step []string) bool { return slices.Equal(step, want) }) {
		t.Fatalf("steps = %v, want one matching %v", steps, want)
	}
}
