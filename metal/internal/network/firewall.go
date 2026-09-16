package network

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

type firewallFamily struct {
	saveCommand    string
	restoreCommand string
	isIPv6         bool
}

// ensureFirewall replaces a namespace filter table only when its desired rules differ.
func ensureFirewall(ctx context.Context, namespace string, configuration vm.FirewallConfiguration) error {
	for _, family := range [...]firewallFamily{
		{saveCommand: "iptables-save", restoreCommand: "iptables-restore"},
		{saveCommand: "ip6tables-save", restoreCommand: "ip6tables-restore", isIPv6: true},
	} {
		desired, err := renderFirewallTable(configuration, family.isIPv6)
		if err != nil {
			return err
		}
		current, err := platform.RunInNetworkNamespace(ctx, namespace, family.saveCommand, "-t", "filter")
		if err != nil {
			return fmt.Errorf("read %s firewall: %w", firewallFamilyName(family.isIPv6), err)
		}
		if normalizeFirewallTable(current) == normalizeFirewallTable(desired) {
			continue
		}

		if err := platform.RunWithInput(ctx, desired, "ip", "netns", "exec", namespace,
			family.restoreCommand, "--wait", "5"); err != nil {
			return fmt.Errorf("apply %s firewall: %w", firewallFamilyName(family.isIPv6), err)
		}
	}
	return nil
}

func renderFirewallTable(configuration vm.FirewallConfiguration, isIPv6 bool) (string, error) {
	forwardPolicy := "ACCEPT"
	if configuration.Enabled {
		forwardPolicy = "DROP"
	}

	var table strings.Builder
	fmt.Fprintf(&table, "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD %s [0:0]\n:OUTPUT ACCEPT [0:0]\n", forwardPolicy)
	if configuration.Enabled {
		table.WriteString("-A FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT\n")
		if err := appendFirewallRules(&table, configuration.Inbound, true, isIPv6); err != nil {
			return "", err
		}
		if err := appendFirewallRules(&table, configuration.Outbound, false, isIPv6); err != nil {
			return "", err
		}
	}
	table.WriteString("COMMIT\n")
	return table.String(), nil
}

func appendFirewallRules(table *strings.Builder, rules []vm.FirewallRule, inbound, isIPv6 bool) error {
	for _, rule := range rules {
		for _, value := range rule.CIDRs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return fmt.Errorf("parse firewall CIDR %q: %w", value, err)
			}
			if prefix.Addr().Is6() != isIPv6 {
				continue
			}
			appendFirewallRule(table, rule, value, inbound, isIPv6)
		}
	}
	return nil
}

func appendFirewallRule(table *strings.Builder, rule vm.FirewallRule, cidr string, inbound, isIPv6 bool) {
	table.WriteString("-A FORWARD")
	if inbound {
		fmt.Fprintf(table, " -s %s -o %s", cidr, tapName)
	} else {
		fmt.Fprintf(table, " -d %s -i %s", cidr, tapName)
	}

	protocol := string(rule.Protocol)
	if rule.Protocol == vm.FirewallProtocolICMP && isIPv6 {
		protocol = "ipv6-icmp"
	}
	if rule.Protocol != vm.FirewallProtocolAny {
		fmt.Fprintf(table, " -p %s", protocol)
	}
	if rule.Ports != "" {
		fmt.Fprintf(table, " -m %s --dport %s", protocol, strings.Replace(rule.Ports, "-", ":", 1))
	}
	table.WriteString(" -j ACCEPT\n")
}

func normalizeFirewallTable(table string) string {
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func firewallFamilyName(isIPv6 bool) string {
	if isIPv6 {
		return "IPv6"
	}
	return "IPv4"
}
