//go:build integration

package network

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestSetVirtualEthernetReplacesAPairLeftByAReleasedVM reproduces a released VM
// that kept its namespace and veth pair. The next VM reuses the user ID, so it
// asks for the same device names. Run it with:
//
//	sudo -E go test -tags integration -run TestSetVirtualEthernetReplacesAPairLeftByAReleasedVM ./internal/network/
func TestSetVirtualEthernetReplacesAPairLeftByAReleasedVM(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a network namespace")
	}

	const userID = 999000
	stale, current := "metal-test-stale", "metal-test-current"
	hostName, guestName := virtualEthernetNames(userID)
	for _, namespace := range []string{stale, current} {
		runOrSkipTest(t, "ip", "netns", "add", namespace)
		t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", namespace).Run() })
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", hostName).Run() })

	ctx := context.Background()
	if err := setVirtualEthernet(ctx, "test-stale", userID, true); err != nil {
		t.Fatalf("seed the stale pair: %v", err)
	}

	// The stale pair holds the device names, but its peer is in another namespace.
	if err := setVirtualEthernet(ctx, "test-current", userID, true); err != nil {
		t.Fatalf("replace the stale pair: %v", err)
	}

	attached, err := namespaceLinkExists(ctx, current, guestName)
	if err != nil {
		t.Fatal(err)
	}
	if !attached {
		t.Fatalf("%s is not in %s, so the stale pair was not replaced", guestName, current)
	}
	// Without the transit address the default route reports an invalid gateway.
	_, namespaceIPAddress := transitAddresses(userID)
	if addresses := namespaceAddresses(t, current, guestName); !strings.Contains(addresses, namespaceIPAddress) {
		t.Errorf("addresses on %s = %q, want %s", guestName, addresses, namespaceIPAddress)
	}
}

// namespaceAddresses returns the addresses of one device inside a namespace.
func namespaceAddresses(t *testing.T, namespace, device string) string {
	t.Helper()
	context, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(context, "ip", "-n", namespace, "-o", "addr", "show", "dev", device).CombinedOutput()
	if err != nil {
		t.Fatalf("read addresses of %s: %v: %s", device, err, output)
	}
	return string(output)
}

// runOrSkipTest runs a setup command and skips the test when it fails.
func runOrSkipTest(t *testing.T, name string, args ...string) {
	t.Helper()
	context, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(context, name, args...).CombinedOutput(); err != nil {
		t.Skipf("setup command %s failed: %v: %s", name, err, output)
	}
}
