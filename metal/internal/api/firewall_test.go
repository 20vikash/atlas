package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestFirewallValidationAcceptsSupportedRules(t *testing.T) {
	request := firewallRequest{
		Enabled: true,
		Inbound: []firewallRuleRequest{{
			Protocol: "tcp", Ports: "22", CIDRs: []string{"203.0.113.0/24", "2001:db8::/32"},
		}},
		Outbound: []firewallRuleRequest{{
			Protocol: "udp", Ports: "8000-9000", CIDRs: []string{"0.0.0.0/0"},
		}},
	}

	if err := request.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFirewallValidationRejectsInvalidRules(t *testing.T) {
	tests := []struct {
		name string
		rule firewallRuleRequest
		want string
	}{
		{"unknown protocol", firewallRuleRequest{Protocol: "gre", CIDRs: []string{"0.0.0.0/0"}}, "protocol"},
		{"ports on ICMP", firewallRuleRequest{Protocol: "icmp", Ports: "8", CIDRs: []string{"0.0.0.0/0"}}, "ports"},
		{"reversed ports", firewallRuleRequest{Protocol: "tcp", Ports: "100-50", CIDRs: []string{"0.0.0.0/0"}}, "ascending"},
		{"leading zero", firewallRuleRequest{Protocol: "tcp", Ports: "022", CIDRs: []string{"0.0.0.0/0"}}, "leading zeros"},
		{"missing CIDR", firewallRuleRequest{Protocol: "any"}, "cidrs"},
		{"host bits", firewallRuleRequest{Protocol: "any", CIDRs: []string{"203.0.113.7/24"}}, "canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (firewallRequest{Inbound: []firewallRuleRequest{test.rule}}).validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFirewallValidationLimitsPrefixEntries(t *testing.T) {
	cidrs := make([]string, maximumFirewallPrefixes+1)
	for index := range cidrs {
		cidrs[index] = fmt.Sprintf("10.0.0.%d/32", index)
	}
	request := firewallRequest{Inbound: []firewallRuleRequest{{Protocol: "any", CIDRs: cidrs}}}

	err := request.validate()
	if err == nil || !strings.Contains(err.Error(), "50 prefix entries") {
		t.Fatalf("error = %v", err)
	}
}
