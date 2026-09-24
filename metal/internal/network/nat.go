package network

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// setMasquerade adds or removes the namespace NAT rule. iptables fails on a
// duplicate add and on a delete for an absent rule, so it checks first.
func setMasquerade(ctx context.Context, namespace, guestVirtualEthernet string, present bool) error {
	prefix := namespaceCommandPrefix(namespace)
	rule := []string{"POSTROUTING", "-o", guestVirtualEthernet, "-j", "MASQUERADE"}

	check := commandWithPrefix(prefix, "iptables", append([]string{"-t", "nat", "-C"}, rule...)...)
	exists, err := ruleExists(ctx, check)
	if err != nil || exists == present {
		return err
	}

	action := "-D"
	if present {
		action = "-A"
	}
	command := commandWithPrefix(prefix, "iptables", append([]string{"-t", "nat", action}, rule...)...)
	return platform.Run(ctx, command[0], command[1:]...)
}

// maximumSegmentSizeRule clamps TCP MSS for SYN packets to the guest.
func maximumSegmentSizeRule(guestVirtualEthernet string) []string {
	return []string{
		"FORWARD", "-o", guestVirtualEthernet,
		"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--clamp-mss-to-pmtu",
	}
}

// ensureMaximumSegmentSizeClamp protects TCP from a guest MTU larger than the veth MTU.
func ensureMaximumSegmentSizeClamp(ctx context.Context, virtualMachineID string, userID uint32) error {
	prefix := namespaceCommandPrefix(namespaceName(virtualMachineID))
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	rule := maximumSegmentSizeRule(guestVirtualEthernet)

	check := commandWithPrefix(prefix, "iptables", append([]string{"-t", "mangle", "-C"}, rule...)...)
	exists, err := ruleExists(ctx, check)
	if err != nil || exists {
		return err
	}

	command := commandWithPrefix(prefix, "iptables", append([]string{"-t", "mangle", "-A"}, rule...)...)
	return platform.Run(ctx, command[0], command[1:]...)
}

// ruleExists runs an iptables check. Exit code 1 means absent. Any other failure
// is an error, so a broken check does not read as absent.
func ruleExists(ctx context.Context, check []string) (bool, error) {
	err := platform.Run(ctx, check[0], check[1:]...)
	if err == nil {
		return true, nil
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func ensurePublicIPv4(ctx context.Context, virtualMachineID string, userID uint32, publicIPv4 string) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	_, namespaceIPAddress := transitAddresses(userID)
	steps := publicIPv4Steps(virtualMachineID, namespace, guestVirtualEthernet, namespaceIPAddress, publicIPv4)
	return ensureRuleSet(ctx, steps, func() error {
		return errors.Join(removePublicIPv4Rules(ctx, virtualMachineID), removePublicIPv4NamespaceRules(ctx, virtualMachineID))
	})
}

// ensureRuleSet replaces the full set when any rule is missing.
func ensureRuleSet(ctx context.Context, steps [][]string, remove func() error) error {
	for _, step := range steps {
		exists, err := ruleExists(ctx, ruleCheck(step))
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := remove(); err != nil {
			return err
		}
		return runSteps(ctx, steps)
	}
	return nil
}

// ruleCheck removes an insert position because iptables -C does not accept it.
func ruleCheck(step []string) []string {
	check := append([]string(nil), step...)
	for index, argument := range check {
		switch argument {
		case "-A":
			check[index] = "-C"
			return check
		case "-I":
			check[index] = "-C"
			return append(check[:index+2], check[index+3:]...)
		}
	}
	return check
}

// publicIPv4Steps maps the public address to the guest: DNAT in for forwarded and
// host-originated traffic, SNAT out, forwarding both ways, and a second DNAT inside
// the namespace. The SNAT rule is inserted first, so it wins over any wider
// masquerade rule.
func publicIPv4Steps(virtualMachineID, namespace, guestVirtualEthernet, namespaceIPAddress, publicIPv4 string) [][]string {
	comment := publicIPv4Comment(virtualMachineID)
	return [][]string{
		// Inbound: the public address becomes the namespace transit address.
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-d", publicIPv4, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", namespaceIPAddress},

		// Host-originated: a local process reaches a co-located VM through its
		// public address. Such a packet skips PREROUTING, so OUTPUT repeats the map.
		{"iptables", "-t", "nat", "-A", "OUTPUT", "-d", publicIPv4, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", namespaceIPAddress},

		// Insert SNAT first so it wins over namespace masquerading.
		{"iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", namespaceIPAddress, "-m", "comment", "--comment", comment, "-j", "SNAT", "--to-source", publicIPv4},

		// Allow new inbound connections and the traffic they establish.
		{"iptables", "-A", "FORWARD", "-d", namespaceIPAddress, "-m", "conntrack", "--ctstate", "NEW,ESTABLISHED,RELATED", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},

		// Allow the return path only. The VM starts no connection through this rule.
		{"iptables", "-A", "FORWARD", "-s", namespaceIPAddress, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},

		// Inside the namespace: the transit address becomes the guest address.
		{"ip", "netns", "exec", namespace, "iptables", "-t", "nat", "-A", "PREROUTING", "-i", guestVirtualEthernet, "-d", namespaceIPAddress, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", guestIPAddress},
	}
}

func removePublicIPv4Rules(ctx context.Context, virtualMachineID string) error {
	return removeTaggedRules(ctx, nil, "iptables", publicIPv4Comment(virtualMachineID))
}

func removePublicIPv4NamespaceRules(ctx context.Context, virtualMachineID string) error {
	return removeTaggedRules(ctx, namespaceCommandPrefix(namespaceName(virtualMachineID)), "iptables", publicIPv4Comment(virtualMachineID))
}

func namespaceCommandPrefix(namespace string) []string {
	return []string{"ip", "netns", "exec", namespace}
}

// removeTaggedRules deletes every rule carrying this comment. It continues past
// a failure and joins the errors, so one bad table does not leave the other
// tables untouched.
func removeTaggedRules(ctx context.Context, prefix []string, command, comment string) error {
	var cleanupErrors []error
	for _, table := range []string{"nat", "filter"} {
		arguments := commandWithPrefix(prefix, command, "-t", table, "-S")
		output, err := platform.Output(ctx, arguments[0], arguments[1:]...)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("list %s rules: %w", table, err))
			continue
		}
		for _, line := range strings.Split(output, "\n") {
			ruleArguments := strings.Fields(line)
			if !hasRuleComment(ruleArguments, comment) {
				continue
			}
			if len(ruleArguments) < 2 || ruleArguments[0] != "-A" {
				continue
			}
			ruleArguments[0] = "-D"
			for index, argument := range ruleArguments {
				ruleArguments[index] = strings.Trim(argument, "\"")
			}
			arguments = commandWithPrefix(prefix, command, "-t", table)
			arguments = append(arguments, ruleArguments...)
			if err := platform.Run(ctx, arguments[0], arguments[1:]...); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s rule: %w", table, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func commandWithPrefix(prefix []string, command string, arguments ...string) []string {
	result := append([]string(nil), prefix...)
	result = append(result, command)
	return append(result, arguments...)
}

func hasRuleComment(arguments []string, expected string) bool {
	for index, argument := range arguments {
		if argument == "--comment" && index+1 < len(arguments) {
			return strings.Trim(arguments[index+1], "\"") == expected
		}
	}
	return false
}

func publicIPv4Comment(virtualMachineID string) string {
	return "metal-public-ipv4-" + virtualMachineID
}
