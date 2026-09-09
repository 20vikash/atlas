package network

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// defaultRouteSteps builds the namespace route and forwarding.
func defaultRouteSteps(hostIPAddress string) [][]string {
	return [][]string{
		{"ip", "route", "replace", "default", "via", hostIPAddress},
		{"sysctl", "-q", "-w", "net.ipv4.ip_forward=1"},
	}
}

// addInternetPath adds the namespace route and NAT rule.
func addInternetPath(ctx context.Context, virtualMachineID string, userID uint32) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	hostIPAddress, _ := transitAddresses(userID)
	if err := runNetworkNamespaceSteps(ctx, namespace, defaultRouteSteps(hostIPAddress)); err != nil {
		return err
	}
	return setMasquerade(ctx, namespace, guestVirtualEthernet, true)
}

// removeInternetPath removes the NAT rule before the route.
func removeInternetPath(ctx context.Context, virtualMachineID string, userID uint32) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	if err := setMasquerade(ctx, namespace, guestVirtualEthernet, false); err != nil {
		return err
	}
	return removeDefaultRoute(ctx, namespace)
}

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

// removeDefaultRoute removes the namespace default route when it is present.
func removeDefaultRoute(ctx context.Context, namespace string) error {
	output, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "route", "show", "default")
	if err != nil {
		return fmt.Errorf("show default route: %w", err)
	}
	if strings.TrimSpace(output) == "" {
		return nil
	}
	_, err = platform.RunInNetworkNamespace(ctx, namespace, "ip", "route", "del", "default")
	return err
}

// ensurePublicIPv4 makes the public address rules present. The rules are one
// set: if any is missing, all are removed and rewritten, so a partial set from
// an interrupted run cannot survive.
func ensurePublicIPv4(ctx context.Context, virtualMachineID string, userID uint32, publicIPv4 string) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	_, namespaceIPAddress := transitAddresses(userID)
	steps := publicIPv4Steps(virtualMachineID, namespace, guestVirtualEthernet, namespaceIPAddress, publicIPv4)

	for _, step := range steps {
		exists, err := ruleExists(ctx, publicIPv4RuleCheck(step))
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := removePublicIPv4Rules(ctx, virtualMachineID); err != nil {
			return err
		}
		if err := removePublicIPv4NamespaceRules(ctx, virtualMachineID); err != nil {
			return err
		}
		return runSteps(ctx, steps)
	}
	return nil
}

// publicIPv4RuleCheck turns an append rule into the -C form that tests for it.
// An insert also carries a position, which -C does not accept.
func publicIPv4RuleCheck(step []string) []string {
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

// publicIPv4Steps maps the public address to the guest: DNAT in, SNAT out,
// forwarding both ways, and a second DNAT inside the namespace. The SNAT rule is
// inserted first, so it wins over any wider masquerade rule.
func publicIPv4Steps(virtualMachineID, namespace, guestVirtualEthernet, namespaceIPAddress, publicIPv4 string) [][]string {
	comment := publicIPv4Comment(virtualMachineID)
	return [][]string{
		// Inbound: the public address becomes the namespace transit address.
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-d", publicIPv4, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", namespaceIPAddress},

		// Outbound: the VM leaves as its public address. Inserted at position 1,
		// so it wins over the wider masquerade rule that egress adds.
		{"iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", namespaceIPAddress, "-m", "comment", "--comment", comment, "-j", "SNAT", "--to-source", publicIPv4},

		// Allow new inbound connections and the traffic they establish.
		{"iptables", "-A", "FORWARD", "-d", namespaceIPAddress, "-m", "conntrack", "--ctstate", "NEW,ESTABLISHED,RELATED", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},

		// Allow the return path only. The VM starts no connection through this rule.
		{"iptables", "-A", "FORWARD", "-s", namespaceIPAddress, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},

		// Inside the namespace: the transit address becomes the guest address.
		{"ip", "netns", "exec", namespace, "iptables", "-t", "nat", "-A", "PREROUTING", "-i", guestVirtualEthernet, "-d", namespaceIPAddress, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", guestIPAddress},
	}
}

// removePublicIPv4Rules removes the host rules of one virtual machine.
func removePublicIPv4Rules(ctx context.Context, virtualMachineID string) error {
	return removePublicIPv4RulesFrom(ctx, nil, virtualMachineID)
}

// removePublicIPv4NamespaceRules removes the namespace rules of one VM.
func removePublicIPv4NamespaceRules(ctx context.Context, virtualMachineID string) error {
	return removePublicIPv4RulesFrom(ctx, namespaceCommandPrefix(namespaceName(virtualMachineID)), virtualMachineID)
}

// namespaceCommandPrefix runs a host command inside one VM network namespace.
func namespaceCommandPrefix(namespace string) []string {
	return []string{"ip", "netns", "exec", namespace}
}

// removePublicIPv4RulesFrom deletes every rule carrying this VM's comment. It
// continues past a failure and joins the errors, so one bad table does not leave
// the other tables untouched.
func removePublicIPv4RulesFrom(ctx context.Context, prefix []string, virtualMachineID string) error {
	var cleanupErrors []error
	for _, table := range []string{"nat", "filter"} {
		arguments := commandWithPrefix(prefix, "iptables", "-t", table, "-S")
		output, err := platform.Output(ctx, arguments[0], arguments[1:]...)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("list %s rules: %w", table, err))
			continue
		}
		for _, line := range strings.Split(output, "\n") {
			ruleArguments := strings.Fields(line)
			if !hasRuleComment(ruleArguments, publicIPv4Comment(virtualMachineID)) {
				continue
			}
			if len(ruleArguments) < 2 || ruleArguments[0] != "-A" {
				continue
			}
			ruleArguments[0] = "-D"
			for index, argument := range ruleArguments {
				ruleArguments[index] = strings.Trim(argument, "\"")
			}
			arguments = commandWithPrefix(prefix, "iptables", "-t", table)
			arguments = append(arguments, ruleArguments...)
			if err := platform.Run(ctx, arguments[0], arguments[1:]...); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s rule: %w", table, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

// commandWithPrefix builds a command, optionally inside a namespace.
func commandWithPrefix(prefix []string, command string, arguments ...string) []string {
	result := append([]string(nil), prefix...)
	result = append(result, command)
	return append(result, arguments...)
}

// hasRuleComment reports whether an `iptables -S` line carries this comment.
func hasRuleComment(arguments []string, expected string) bool {
	for index, argument := range arguments {
		if argument == "--comment" && index+1 < len(arguments) {
			return strings.Trim(arguments[index+1], "\"") == expected
		}
	}
	return false
}

// publicIPv4Comment tags every rule of one VM, so cleanup can find them all
// without keeping any rule state of its own.
func publicIPv4Comment(virtualMachineID string) string {
	return "metal-public-ipv4-" + virtualMachineID
}
