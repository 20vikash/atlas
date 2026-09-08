//go:build linux && integration

package activity

import (
	"context"
	"io"
	"log/slog"
	"net"
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

// ipv4Frame builds one Ethernet frame that carries a minimal IPv4 header with
// the given protocol. The activity hook reads only the ethertype and the IP
// protocol, so the header does not need valid addresses or a checksum.
func ipv4Frame(protocol byte) []byte {
	header := make([]byte, 20)
	header[0] = 0x45 // version 4, header length 5 words
	header[9] = protocol
	return ethernetFrame(0x0800, header) // 0x0800 is the IPv4 ethertype
}

// sendHostToGuest sends one packet toward the guest from inside the namespace.
// The kernel routes it out on tap0 egress, which is the host-to-guest direction.
// A TCP dial sends one SYN even with no listener. A UDP dial sends one datagram.
func sendHostToGuest(t *testing.T, namespacePath, transport string) {
	t.Helper()
	address := net.JoinHostPort(guestIPAddress, "9")
	err := inNamespace(osNamespaceSyscalls{}, namespacePath, func() error {
		switch transport {
		case "tcp":
			connection, _ := net.DialTimeout("tcp", address, 300*time.Millisecond)
			if connection != nil {
				_ = connection.Close()
			}
		case "udp":
			connection, dialError := net.Dial("udp", address)
			if dialError != nil {
				return dialError
			}
			_, _ = connection.Write([]byte{0})
			_ = connection.Close()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("send %s toward guest: %v", transport, err)
	}
}

// waitForFreshActivity polls until the last-seen time moves past mark.
func waitForFreshActivity(t *testing.T, monitor *Monitor, request vm.NetworkActivityRequest, mark time.Time) vm.NetworkActivity {
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

// assertNoActivity fails if any packet registers within a short window.
func assertNoActivity(t *testing.T, monitor *Monitor, request vm.NetworkActivityRequest) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	activity, err := monitor.LastNetworkActivity(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if activity.HasBeenSeen {
		t.Fatal("only a host-to-guest TCP segment must register as activity")
	}
}

// TestActivityCountsHostToGuestTcpOnly proves the activity hook counts only a
// host-to-guest TCP segment. A guest-to-host frame does not count, because the
// guest's own traffic must not keep a sleepy VM awake. A host-to-guest UDP
// datagram does not count either, so link-local housekeeping such as IPv6 MLD
// and ARP does not keep the VM awake. It needs root, the ip command, and TCX
// support. Run it with:
//
//	sudo -E go test -tags integration -run TestActivityCountsHostToGuestTcpOnly ./internal/network/
func TestActivityCountsHostToGuestTcpOnly(t *testing.T) {
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
	// Quiet the tap IPv6 stack, so no kernel-generated egress frame advances
	// activity and hides the guest-to-host result.
	runOrSkip(t, "ip", "netns", "exec", namespace, "sysctl", "-q", "-w", "net.ipv6.conf."+tapName+".disable_ipv6=1")
	runOrSkip(t, "ip", "-n", namespace, "neigh", "replace", guestIPAddress, "lladdr", guestMACAddress, "dev", tapName, "nud", "permanent")

	monitor, err := NewMonitor(MonitorConfig{
		UserIDRange: vm.DefaultUserIDRange,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	t.Cleanup(func() { _ = monitor.Close() })

	if err := monitor.EnsureAttachment(AttachmentRequest{VirtualMachineID: "vm-dir", UserID: userID, NamespacePath: namespacePath, TapName: tapName}); err != nil {
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

	// A guest-to-host TCP frame written into the tap enters the kernel on tap0
	// ingress. It must not count, because the guest's own traffic must not keep it
	// awake, even when it is TCP.
	if _, err := unix.Write(tapQueue, ipv4Frame(6)); err != nil {
		t.Fatalf("write guest frame: %v", err)
	}
	assertNoActivity(t, monitor, request)

	// A host-to-guest UDP datagram leaves on tap0 egress but is not TCP. It must
	// not count, so housekeeping traffic does not keep the VM awake.
	sendHostToGuest(t, namespacePath, "udp")
	assertNoActivity(t, monitor, request)

	// A host-to-guest TCP SYN leaves on tap0 egress and counts.
	sendHostToGuest(t, namespacePath, "tcp")
	waitForFreshActivity(t, monitor, request, baseline.LastSeenAt)
}
