//go:build linux && integration

package network

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"golang.org/x/sys/unix"
)

// sharedHashMap creates one shared hash map from the spec with the given
// capacity. The test owns it and closes it.
func sharedHashMap(t *testing.T, spec *ebpf.CollectionSpec, name string, capacity uint32) *ebpf.Map {
	t.Helper()
	mapSpec := spec.Maps[name].Copy()
	mapSpec.MaxEntries = capacity
	handle, err := ebpf.NewMap(mapSpec)
	if err != nil {
		t.Fatalf("create shared map %s: %v", name, err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// sharedRingMap creates one shared ring buffer map from the spec. The test owns
// it and closes it.
func sharedRingMap(t *testing.T, spec *ebpf.CollectionSpec, name string) *ebpf.Map {
	t.Helper()
	handle, err := ebpf.NewMap(spec.Maps[name].Copy())
	if err != nil {
		t.Fatalf("create shared ring map %s: %v", name, err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// loadWakeProgram loads the activity program with its three maps replaced by the
// given shared maps. It rewrites the user ID constant, like the real loader.
func loadWakeProgram(t *testing.T, spec *ebpf.CollectionSpec, userID uint32, activity, wakeState, wakeEvents *ebpf.Map) *ebpf.Program {
	t.Helper()
	if err := spec.Variables["virtual_machine_user_id"].Set(userID); err != nil {
		t.Fatalf("set user id: %v", err)
	}
	// Match the small C capacities to the shared maps, so the replacement passes
	// the compatibility check.
	spec.Maps["activity_by_user_id"].MaxEntries = activity.MaxEntries()
	spec.Maps["wake_state_by_user_id"].MaxEntries = wakeState.MaxEntries()

	var programs activityPrograms
	err := spec.LoadAndAssign(&programs, &ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{
			"activity_by_user_id":   activity,
			"wake_state_by_user_id": wakeState,
			"wake_events":           wakeEvents,
		},
	})
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Fatalf("verifier rejected the wake program:\n%+v", verifier)
		}
		t.Fatalf("load wake program: %v", err)
	}
	t.Cleanup(func() { _ = programs.RecordActivity.Close() })
	return programs.RecordActivity
}

// TestWakeProgramLoadsWithSharedMaps loads the wake program into the kernel and
// replaces its three maps with shared maps. It proves the program verifies and
// the shared map definitions are compatible replacements. It needs root.
//
//	sudo -E go test -tags integration -run TestWakeProgramLoadsWithSharedMaps ./internal/network/
func TestWakeProgramLoadsWithSharedMaps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	spec, err := loadActivity()
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	const capacity = 1024
	activity := sharedHashMap(t, spec, "activity_by_user_id", capacity)
	wakeState := sharedHashMap(t, spec, "wake_state_by_user_id", capacity)
	wakeEvents := sharedRingMap(t, spec, "wake_events")

	loadWakeProgram(t, spec, 100001, activity, wakeState, wakeEvents)
}

// wakeHarness holds one armed VM: a namespace with tap0, the wake program on the
// tap0 egress hook, and the shared wake state and ring buffer maps.
type wakeHarness struct {
	namespacePath string
	userID        uint32
	wakeState     *ebpf.Map
	wakeEvents    *ebpf.Map
}

// newWakeHarness prepares a namespace with tap0, loads the wake program with
// shared maps, and attaches it to the tap0 egress hook. ringBytes sets the ring
// buffer size, so a pressure test can use a small ring.
func newWakeHarness(t *testing.T, namespace string, userID, ringBytes uint32) wakeHarness {
	t.Helper()
	namespacePath := "/run/netns/" + namespace

	runOrSkip(t, "ip", "netns", "add", namespace)
	t.Cleanup(func() { _ = runQuietly("ip", "netns", "del", namespace) })
	runOrSkip(t, "ip", "-n", namespace, "tuntap", "add", tapName, "mode", "tap")
	runOrSkip(t, "ip", "-n", namespace, "addr", "add", "172.16.0.1/24", "dev", tapName)
	runOrSkip(t, "ip", "-n", namespace, "link", "set", tapName, "up")
	runOrSkip(t, "ip", "-n", namespace, "link", "set", "lo", "up")
	runOrSkip(t, "ip", "netns", "exec", namespace, "sysctl", "-q", "-w", "net.ipv6.conf."+tapName+".disable_ipv6=1")
	runOrSkip(t, "ip", "-n", namespace, "neigh", "replace", guestIPAddress, "lladdr", guestMACAddress, "dev", tapName, "nud", "permanent")

	spec, err := loadActivity()
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	activity := sharedHashMap(t, spec, "activity_by_user_id", 1024)
	wakeState := sharedHashMap(t, spec, "wake_state_by_user_id", 1024)
	spec.Maps["wake_events"].MaxEntries = ringBytes
	wakeEvents := sharedRingMap(t, spec, "wake_events")

	program := loadWakeProgram(t, spec, userID, activity, wakeState, wakeEvents)
	attachEgress(t, program, namespacePath)

	return wakeHarness{namespacePath: namespacePath, userID: userID, wakeState: wakeState, wakeEvents: wakeEvents}
}

// attachEgress attaches the program to the tap0 egress hook inside the namespace.
func attachEgress(t *testing.T, program *ebpf.Program, namespacePath string) {
	t.Helper()
	err := inNamespace(osNamespaceSyscalls{}, namespacePath, func() error {
		device, err := net.InterfaceByName(tapName)
		if err != nil {
			return err
		}
		egress, err := link.AttachTCX(link.TCXOptions{
			Program:   program,
			Attach:    ebpf.AttachTCXEgress,
			Interface: device.Index,
		})
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = egress.Close() })
		return nil
	})
	if err != nil {
		t.Fatalf("attach egress: %v", err)
	}
}

// setWakeState writes one wake state for the VM user ID.
func setWakeState(t *testing.T, wakeState *ebpf.Map, userID, state uint32) {
	t.Helper()
	if err := wakeState.Update(userID, state, ebpf.UpdateAny); err != nil {
		t.Fatalf("set wake state: %v", err)
	}
}

// lookupWakeState reads the current wake state for the VM user ID.
func lookupWakeState(t *testing.T, wakeState *ebpf.Map, userID uint32) uint32 {
	t.Helper()
	var state uint32
	if err := wakeState.Lookup(userID, &state); err != nil {
		t.Fatalf("read wake state: %v", err)
	}
	return state
}

// trySendTcpSyn sends one TCP SYN toward the guest from inside the namespace. It
// uses a non-blocking connect, so it returns as soon as the SYN leaves on tap0.
func trySendTcpSyn(namespacePath string) error {
	return inNamespace(osNamespaceSyscalls{}, namespacePath, func() error {
		fileDescriptor, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer unix.Close(fileDescriptor)

		var address unix.SockaddrInet4
		address.Port = 9
		copy(address.Addr[:], net.ParseIP(guestIPAddress).To4())
		if err := unix.Connect(fileDescriptor, &address); err != nil && !errors.Is(err, unix.EINPROGRESS) {
			return err
		}
		return nil
	})
}

// readWakeUserID reads one wake event and returns its user ID. ok is false when
// no event arrives before the deadline.
func readWakeUserID(t *testing.T, reader *ringbuf.Reader, wait time.Duration) (uint32, bool) {
	t.Helper()
	reader.SetDeadline(time.Now().Add(wait))
	record, err := reader.Read()
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("read wake event: %v", err)
	}
	if len(record.RawSample) < 16 {
		t.Fatalf("wake event sample is %d bytes, want 16", len(record.RawSample))
	}
	return binary.LittleEndian.Uint32(record.RawSample[0:4]), true
}

// TestWakeDeduplicatesConcurrentPackets proves that many packets on an armed VM
// create one wake event, and that a rearm is needed before the next event. It
// needs root.
//
//	sudo -E go test -tags integration -run TestWakeDeduplicatesConcurrentPackets ./internal/network/
func TestWakeDeduplicatesConcurrentPackets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	harness := newWakeHarness(t, "metal-test-wake-dedup", 100003, 1<<16)
	reader, err := ringbuf.NewReader(harness.wakeEvents)
	if err != nil {
		t.Fatalf("open ring reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	setWakeState(t, harness.wakeState, harness.userID, wakeArmed)

	// Send many packets at the same time.
	const packets = 24
	sendErrors := make(chan error, packets)
	var group sync.WaitGroup
	for i := 0; i < packets; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			sendErrors <- trySendTcpSyn(harness.namespacePath)
		}()
	}
	group.Wait()
	close(sendErrors)
	for err := range sendErrors {
		if err != nil {
			t.Fatalf("send SYN: %v", err)
		}
	}

	// Exactly one event, then none until rearm.
	userID, ok := readWakeUserID(t, reader, time.Second)
	if !ok {
		t.Fatal("no wake event for the first armed packet")
	}
	if userID != harness.userID {
		t.Fatalf("wake event user id = %d, want %d", userID, harness.userID)
	}
	if _, ok := readWakeUserID(t, reader, 200*time.Millisecond); ok {
		t.Fatal("a second wake event arrived without a rearm")
	}
	if state := lookupWakeState(t, harness.wakeState, harness.userID); state != wakeNotified {
		t.Fatalf("wake state = %d, want notified", state)
	}

	// A rearm allows one more event.
	setWakeState(t, harness.wakeState, harness.userID, wakeArmed)
	if err := trySendTcpSyn(harness.namespacePath); err != nil {
		t.Fatalf("send SYN after rearm: %v", err)
	}
	if _, ok := readWakeUserID(t, reader, time.Second); !ok {
		t.Fatal("no wake event after rearm")
	}
}

// TestWakeSurvivesRingPressure proves a full ring buffer never loses wake intent.
// It fills a small ring without reading, then confirms a rearmed VM still
// notifies after the ring drains. It needs root.
//
//	sudo -E go test -tags integration -run TestWakeSurvivesRingPressure ./internal/network/
func TestWakeSurvivesRingPressure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	// 4096 bytes is the smallest page-aligned ring, so few events fill it.
	harness := newWakeHarness(t, "metal-test-wake-pressure", 100004, 4096)

	// Rearm and send without a reader, so events pile up until the ring is full.
	const cycles = 400
	for i := 0; i < cycles; i++ {
		setWakeState(t, harness.wakeState, harness.userID, wakeArmed)
		if err := trySendTcpSyn(harness.namespacePath); err != nil {
			t.Fatalf("send SYN in fill cycle %d: %v", i, err)
		}
	}

	reader, err := ringbuf.NewReader(harness.wakeEvents)
	if err != nil {
		t.Fatalf("open ring reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	// The ring holds a bounded number of events, well below the cycle count.
	buffered := 0
	for {
		if _, ok := readWakeUserID(t, reader, 300*time.Millisecond); !ok {
			break
		}
		buffered++
	}
	if buffered == 0 {
		t.Fatal("no wake events buffered under pressure")
	}
	if buffered >= cycles {
		t.Fatalf("buffered %d events, want a bounded number below %d", buffered, cycles)
	}

	// After the ring drains, a rearmed VM still notifies.
	setWakeState(t, harness.wakeState, harness.userID, wakeArmed)
	if err := trySendTcpSyn(harness.namespacePath); err != nil {
		t.Fatalf("send SYN after drain: %v", err)
	}
	if _, ok := readWakeUserID(t, reader, time.Second); !ok {
		t.Fatal("no wake event after the ring drained")
	}
}
