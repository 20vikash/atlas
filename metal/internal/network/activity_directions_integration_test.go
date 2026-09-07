//go:build linux && integration

package network

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/frappe/atlas/metal/internal/vm"
)

// ifreq is the interface request for the TUNSETIFF ioctl.
type ifreq struct {
	name  [16]byte
	flags uint16
	_     [22]byte
}

// openTapQueue attaches to an existing tap0 inside the namespace and returns a
// queue file descriptor. It runs the ioctl inside the target namespace, because
// the tap belongs to that namespace.
func openTapQueue(t *testing.T, namespacePath, name string) int {
	t.Helper()
	var fileDescriptor int
	err := inNamespace(osNamespaceSyscalls{}, namespacePath, func() error {
		descriptor, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		var request ifreq
		copy(request.name[:], name)
		request.flags = unix.IFF_TAP | unix.IFF_NO_PI
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(descriptor), uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&request))); errno != 0 {
			unix.Close(descriptor)
			return errno
		}
		fileDescriptor = descriptor
		return nil
	})
	if err != nil {
		t.Fatalf("open tap queue: %v", err)
	}
	return fileDescriptor
}

// ethernetFrame builds one Ethernet frame with the given ethertype and payload.
func ethernetFrame(ethertype uint16, payload []byte) []byte {
	frame := []byte{
		0x06, 0x00, 0xac, 0x10, 0x00, 0x02, // destination MAC
		0x06, 0x00, 0xac, 0x10, 0x00, 0x01, // source MAC
		byte(ethertype >> 8), byte(ethertype),
	}
	return append(frame, payload...)
}

// waitForFreshActivity polls until the last-seen time moves past mark.
func waitForFreshActivity(t *testing.T, monitor *ActivityMonitor, request vm.NetworkActivityRequest, mark time.Time) vm.NetworkActivity {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		activity, err := monitor.LastNetworkActivity(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if activity.HasBeenSeen && activity.LastSeenAt.After(mark) {
			return activity
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("activity did not advance in time")
	return vm.NetworkActivity{}
}

// TestActivityCountsBothPacketDirections proves a frame in each direction over
// tap0 advances the activity time. It needs root, the ip command, and TCX
// support. Run it with:
//
//	sudo -E go test -tags integration -run TestActivityCountsBothPacketDirections ./internal/network/
func TestActivityCountsBothPacketDirections(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	namespace := "metal-test-dir"
	namespacePath := "/run/netns/" + namespace
	const userID = 100001
	request := vm.NetworkActivityRequest{VirtualMachineID: "vm-dir", UserID: userID}

	runOrSkip(t, "ip", "netns", "add", namespace)
	t.Cleanup(func() { _ = runQuietly("ip", "netns", "del", namespace) })
	runOrSkip(t, "ip", "-n", namespace, "tuntap", "add", tapName, "mode", "tap")
	runOrSkip(t, "ip", "-n", namespace, "addr", "add", "172.16.0.1/24", "dev", tapName)
	runOrSkip(t, "ip", "-n", namespace, "link", "set", tapName, "up")
	runOrSkip(t, "ip", "-n", namespace, "link", "set", "lo", "up")
	runOrSkip(t, "ip", "-n", namespace, "neigh", "replace", guestIPAddress, "lladdr", guestMACAddress, "dev", tapName, "nud", "permanent")

	monitor, err := NewActivityMonitor(ActivityMonitorConfig{
		UserIDRange: vm.DefaultUserIDRange,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	t.Cleanup(func() { _ = monitor.Close() })

	if err := monitor.EnsureAttachment(AttachmentRequest{VirtualMachineID: "vm-dir", UserID: userID, NamespacePath: namespacePath}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	baseline, err := monitor.LastNetworkActivity(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.HasBeenSeen {
		t.Fatal("a new attachment must not report a packet yet")
	}

	tapQueue := openTapQueue(t, namespacePath, tapName)
	t.Cleanup(func() { _ = unix.Close(tapQueue) })

	// Ingress: a frame written into the tap enters the kernel on tap0.
	arpPayload := make([]byte, 28)
	if _, err := unix.Write(tapQueue, ethernetFrame(0x0806, arpPayload)); err != nil {
		t.Fatalf("write ingress frame: %v", err)
	}
	afterIngress := waitForFreshActivity(t, monitor, request, baseline.LastSeenAt)

	// A malformed, non-IP frame must also count and must not block the write.
	if _, err := unix.Write(tapQueue, ethernetFrame(0x88b5, []byte{0x01, 0x02, 0x03})); err != nil {
		t.Fatalf("write malformed frame: %v", err)
	}
	waitForFreshActivity(t, monitor, request, afterIngress.LastSeenAt)

	// Egress: a packet from the namespace stack toward the guest leaves on tap0.
	mark, err := monitor.LastNetworkActivity(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	_ = runQuietly("ip", "netns", "exec", namespace, "ping", "-c", "1", "-W", "1", guestIPAddress)
	waitForFreshActivity(t, monitor, request, mark.LastSeenAt)
}
