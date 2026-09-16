//go:build integration

package network

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// TestEnsureFirewallReplacesDrift verifies the host iptables restore path. Run it with:
//
//	sudo -E go test -tags integration -run TestEnsureFirewallReplacesDrift ./internal/network/
func TestEnsureFirewallReplacesDrift(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a network namespace")
	}

	const namespace = "metal-test-firewall"
	runOrSkipTest(t, "ip", "netns", "add", namespace)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", namespace).Run() })

	configuration := vm.FirewallConfiguration{
		Enabled: true,
		Inbound: []vm.FirewallRule{{
			Protocol: vm.FirewallProtocolTCP,
			Ports:    "22",
			CIDRs:    []string{"203.0.113.0/24", "2001:db8::/32"},
		}},
	}
	if err := ensureFirewall(context.Background(), namespace, configuration); err != nil {
		t.Fatal(err)
	}
	assertFirewallRule(t, namespace, "iptables-save", "-s 203.0.113.0/24", "--dport 22")
	assertFirewallRule(t, namespace, "ip6tables-save", "-s 2001:db8::/32", "--dport 22")

	runOrSkipTest(t, "ip", "netns", "exec", namespace, "iptables", "-I", "FORWARD", "1", "-j", "ACCEPT")
	if err := ensureFirewall(context.Background(), namespace, configuration); err != nil {
		t.Fatal(err)
	}
	output, err := platform.RunInNetworkNamespace(context.Background(), namespace, "iptables-save", "-t", "filter")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "-A FORWARD -j ACCEPT") {
		t.Fatalf("drift rule remains:\n%s", output)
	}
}

func TestEnsureFirewallRollsBackIPv4WhenIPv6Fails(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a network namespace")
	}

	const namespace = "metal-test-firewall-rollback"
	runOrSkipTest(t, "ip", "netns", "add", namespace)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", namespace).Run() })

	if err := ensureFirewall(context.Background(), namespace, vm.FirewallConfiguration{}); err != nil {
		t.Fatal(err)
	}
	err := ensureFirewallFamilies(
		context.Background(),
		namespace,
		vm.FirewallConfiguration{Enabled: true},
		[]firewallFamily{
			{saveCommand: "iptables-save", restoreCommand: "iptables-restore"},
			{saveCommand: "ip6tables-save", restoreCommand: "false", isIPv6: true},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "apply IPv6 firewall") {
		t.Fatalf("error = %v, want IPv6 apply failure", err)
	}

	output, err := platform.RunInNetworkNamespace(context.Background(), namespace, "iptables-save", "-t", "filter")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, ":FORWARD ACCEPT") {
		t.Fatalf("IPv4 firewall was not rolled back:\n%s", output)
	}
}

func assertFirewallRule(t *testing.T, namespace, command string, fragments ...string) {
	t.Helper()
	output, err := platform.RunInNetworkNamespace(context.Background(), namespace, command, "-t", "filter")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("%s output does not contain %q:\n%s", command, fragment, output)
		}
	}
}
