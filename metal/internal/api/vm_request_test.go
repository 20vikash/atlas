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

func TestGatewayValidationAcceptsAGatewayAndItsUser(t *testing.T) {
	tests := []networkRequest{
		{
			GatewayRoutes: []gatewayRouteRequest{
				{Destination: "::/0", Gateway: "fdaa:1::49"},
				{Destination: "fdac::/16", Gateway: "fdaa:1::7f"},
			},
			WireGuardMeshIPv6: "fdaa:1::4a",
		},
		{IsNetworkGateway: true, WireGuardMeshIPv6: "fdaa:1::49"},
		{PublicIPv6: "2001:bc8:702:653::/64", WireGuardMeshIPv6: "fdaa:1::4a"},
	}
	for _, request := range tests {
		if err := request.validateMeshRouting(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGatewayValidationRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		request networkRequest
		want    string
	}{
		{
			"public gateway",
			networkRequest{
				GatewayRoutes:     []gatewayRouteRequest{{Destination: "::/0", Gateway: "2001:db8::1"}},
				WireGuardMeshIPv6: "fdaa:1::4a",
			},
			"fdaa::/16",
		},
		{
			"destination with host bits",
			networkRequest{
				GatewayRoutes:     []gatewayRouteRequest{{Destination: "fdac::5/16", Gateway: "fdaa:1::7f"}},
				WireGuardMeshIPv6: "fdaa:1::4a",
			},
			"canonical",
		},
		{
			"routes without a mesh address",
			networkRequest{GatewayRoutes: []gatewayRouteRequest{{Destination: "::/0", Gateway: "fdaa:1::49"}}},
			"wireguard_mesh_ipv6",
		},
		{"gateway without a mesh address", networkRequest{IsNetworkGateway: true}, "wireguard_mesh_ipv6"},
		{"block without a mesh address", networkRequest{PublicIPv6: "2001:db8::/64"}, "wireguard_mesh_ipv6"},
		{
			"host bits",
			networkRequest{PublicIPv6: "2001:db8::5/64", WireGuardMeshIPv6: "fdaa:1::49"},
			"canonical",
		},
		{
			"upper case",
			networkRequest{PublicIPv6: "2001:DB8::/64", WireGuardMeshIPv6: "fdaa:1::49"},
			"canonical",
		},
		{
			"IPv4 block",
			networkRequest{PublicIPv6: "203.0.113.0/24", WireGuardMeshIPv6: "fdaa:1::49"},
			"canonical",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.validateMeshRouting()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
