package network

import (
	"context"
	"errors"
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

type firewallTableUpdate struct {
	family  firewallFamily
	current string
	desired string
}

// ensureFirewall replaces a namespace filter table only when its desired rules differ.
func ensureFirewall(ctx context.Context, namespace string, configuration vm.FirewallConfiguration) error {
	return ensureFirewallFamilies(ctx, namespace, configuration, []firewallFamily{
		{saveCommand: "iptables-save", restoreCommand: "iptables-restore"},
		{saveCommand: "ip6tables-save", restoreCommand: "ip6tables-restore", isIPv6: true},
	})
}

func ensureFirewallFamilies(
	ctx context.Context,
	namespace string,
	configuration vm.FirewallConfiguration,
	families []firewallFamily,
) error {
	updates := make([]firewallTableUpdate, 0, len(families))
	for _, family := range families {
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
		updates = append(updates, firewallTableUpdate{family: family, current: current, desired: desired})
	}

	applied := make([]firewallTableUpdate, 0, len(updates))
	for _, update := range updates {
		if err := restoreFirewallTable(ctx, namespace, update.family, update.desired); err != nil {
			applyError := fmt.Errorf("apply %s firewall: %w", firewallFamilyName(update.family.isIPv6), err)
			return errors.Join(applyError, rollbackFirewallTables(ctx, namespace, applied))
		}
		applied = append(applied, update)
	}
	return nil
}

func rollbackFirewallTables(
	ctx context.Context,
	namespace string,
	updates []firewallTableUpdate,
) error {
	var rollbackErrors []error
	for index := len(updates) - 1; index >= 0; index-- {
		update := updates[index]
		if err := restoreFirewallTable(ctx, namespace, update.family, update.current); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf(
				"roll back %s firewall: %w",
				firewallFamilyName(update.family.isIPv6),
				err,
			))
		}
	}
	return errors.Join(rollbackErrors...)
}

func restoreFirewallTable(
	ctx context.Context,
	namespace string,
	family firewallFamily,
	table string,
) error {
	return platform.RunWithInput(ctx, table, "ip", "netns", "exec", namespace,
		family.restoreCommand, "--wait", "5")
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
