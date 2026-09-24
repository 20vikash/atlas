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

	if check := ruleCheck(step); !slices.Equal(check, want) {
		t.Fatalf("rule check = %v, want %v", check, want)
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

func TestMaximumSegmentSizeRuleClampsBothDirections(t *testing.T) {
	rule := maximumSegmentSizeRule("vg-100000")

	want := []string{
		"FORWARD", "-o", "vg-100000",
		"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--clamp-mss-to-pmtu",
	}
	if !slices.Equal(rule, want) {
		t.Fatalf("rule = %v, want %v", rule, want)
	}
}
