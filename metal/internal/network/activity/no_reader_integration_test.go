//go:build linux && integration

package activity

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/frappe/atlas/metal/internal/vm"
)

// TestActivityAdvancesWithoutATapReader checks activity without a tap reader.
//
//	sudo -E go test -tags integration -run TestActivityAdvancesWithoutATapReader ./internal/network/activity/
func TestActivityAdvancesWithoutATapReader(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	namespace := "metal-test-noreader"
	namespacePath := "/run/netns/" + namespace
	const userID = 100002
	request := vm.NetworkActivityRequest{VirtualMachineID: "vm-noreader", UserID: userID}

	// A persistent tap has no queue, so no process reads it.
	runOrSkip(t, "ip", "netns", "add", namespace)
	t.Cleanup(func() { _ = runQuietly("ip", "netns", "del", namespace) })
	runOrSkip(t, "ip", "-n", namespace, "tuntap", "add", tapName, "mode", "tap")
	runOrSkip(t, "ip", "-n", namespace, "addr", "add", "172.16.0.1/24", "dev", tapName)
	runOrSkip(t, "ip", "-n", namespace, "link", "set", tapName, "up")
	runOrSkip(t, "ip", "-n", namespace, "link", "set", "lo", "up")
	runOrSkip(t, "ip", "-n", namespace, "neigh", "replace", guestIPAddress, "lladdr", guestMACAddress, "dev", tapName, "nud", "permanent")

	monitor, err := NewMonitor(MonitorConfig{
		UserIDRange: vm.DefaultUserIDRange,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	t.Cleanup(func() { _ = monitor.Close() })

	if err := monitor.EnsureAttachment(AttachmentRequest{VirtualMachineID: "vm-noreader", UserID: userID, NamespacePath: namespacePath, TapName: tapName}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	baseline, err := monitor.LastNetworkActivity(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.HasBeenSeen {
		t.Fatal("a new attachment must not report a packet yet")
	}

	var kernel unix.Utsname
	_ = unix.Uname(&kernel)
	release := unix.ByteSliceToString(kernel.Release[:])

	// Send a host-to-guest TCP SYN with no tap reader present.
	sendHostToGuest(t, namespacePath, "tcp")

	activity := waitForFreshActivity(t, monitor, request, baseline.LastSeenAt)
	t.Logf("kernel %s: egress hook updated activity without a tap reader; last seen %v", release, activity.LastSeenAt)
}
