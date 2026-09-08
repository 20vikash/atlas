package activity

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

// activityRequest is the read request used by the time-conversion tests.
var activityRequest = vm.NetworkActivityRequest{VirtualMachineID: "vm-1", UserID: 100001}

// readMonitor builds a monitor with a fixed wall clock and one attached VM.
func readMonitor(t *testing.T, wallNow time.Time, monotonicNow uint64) (*Monitor, *fakeActivityMap) {
	t.Helper()
	loader := &fakeActivityLoader{}
	monitor := newTestMonitor(t, loader)
	monitor.wallClock = func() time.Time { return wallNow }
	monitor.monotonicClock = func() (uint64, error) { return monotonicNow, nil }
	monitor.attachments["vm-1"] = &attachment{userID: 100001, baseline: wallNow, program: &fakeActivityProgram{}}
	monitor.byUserID[100001] = "vm-1"
	return monitor, loader.createdMap
}

// fakeActivityMap records how the monitor reads, closes, and clears the map.
type fakeActivityMap struct {
	lookupValue    uint64
	lookupFound    bool
	lookupErr      error
	closed         bool
	deletedUserIDs []uint32
}

func (m *fakeActivityMap) lookup(uint32) (uint64, bool, error) {
	return m.lookupValue, m.lookupFound, m.lookupErr
}

func (m *fakeActivityMap) delete(userID uint32) error {
	m.deletedUserIDs = append(m.deletedUserIDs, userID)
	return nil
}

func (m *fakeActivityMap) Close() error {
	m.closed = true
	return nil
}

// fakeWakeStateMap records the wake states the monitor writes and clears.
type fakeWakeStateMap struct {
	states         map[uint32]uint32
	deletedUserIDs []uint32
	setErr         error
	closed         bool
}

func (m *fakeWakeStateMap) setState(userID, state uint32) error {
	if m.setErr != nil {
		return m.setErr
	}
	if m.states == nil {
		m.states = map[uint32]uint32{}
	}
	m.states[userID] = state
	return nil
}

func (m *fakeWakeStateMap) deleteState(userID uint32) error {
	m.deletedUserIDs = append(m.deletedUserIDs, userID)
	delete(m.states, userID)
	return nil
}

func (m *fakeWakeStateMap) Close() error {
	m.closed = true
	return nil
}

// fakeWakeEventMap serves wake events from a test channel.
type fakeWakeEventMap struct {
	events chan fakeWakeEvent
	closed bool
}

type fakeWakeEvent struct {
	userID     uint32
	packetTime uint64
}

func (m *fakeWakeEventMap) newReader() (wakeEventReader, error) {
	return &fakeWakeEventReader{events: m.events, done: make(chan struct{})}, nil
}

func (m *fakeWakeEventMap) Close() error {
	m.closed = true
	return nil
}

// fakeWakeEventReader returns queued events and stops on close.
type fakeWakeEventReader struct {
	events chan fakeWakeEvent
	done   chan struct{}
	closed bool
}

func (r *fakeWakeEventReader) read() (uint32, uint64, error) {
	select {
	case event, ok := <-r.events:
		if !ok {
			return 0, 0, io.EOF
		}
		return event.userID, event.packetTime, nil
	case <-r.done:
		return 0, 0, io.EOF
	}
}

func (r *fakeWakeEventReader) Close() error {
	if !r.closed {
		r.closed = true
		close(r.done)
	}
	return nil
}

// fakeActivityProgram records how the monitor attaches and closes the program.
type fakeActivityProgram struct {
	attachErr      error
	attachedPaths  []string
	attachedTaps   []string
	interfaceIndex int
	closed         bool
}

func (p *fakeActivityProgram) attach(namespacePath, tapName string) (int, error) {
	if p.attachErr != nil {
		return 0, p.attachErr
	}
	p.attachedPaths = append(p.attachedPaths, namespacePath)
	p.attachedTaps = append(p.attachedTaps, tapName)
	return p.interfaceIndex, nil
}

func (p *fakeActivityProgram) Close() error {
	p.closed = true
	return nil
}

// fakeActivityLoader stands in for the kernel so tests need no root.
type fakeActivityLoader struct {
	createErr        error
	loadErr          error
	attachErr        error
	resolveErr       error
	resolveIndex     int
	attachIndex      int
	createdMap       *fakeActivityMap
	createdWakeState *fakeWakeStateMap
	createdWakeEvent *fakeWakeEventMap
	wakeEvents       chan fakeWakeEvent
	capacities       []uint32
	loadedUserIDs    []uint32
	programs         []*fakeActivityProgram
}

func (loader *fakeActivityLoader) createSharedMaps(capacity uint32) (sharedMaps, error) {
	if loader.createErr != nil {
		return sharedMaps{}, loader.createErr
	}
	loader.capacities = append(loader.capacities, capacity)
	loader.createdMap = &fakeActivityMap{}
	loader.createdWakeState = &fakeWakeStateMap{}
	if loader.wakeEvents == nil {
		loader.wakeEvents = make(chan fakeWakeEvent)
	}
	loader.createdWakeEvent = &fakeWakeEventMap{events: loader.wakeEvents}
	return sharedMaps{
		activity:   loader.createdMap,
		wakeState:  loader.createdWakeState,
		wakeEvents: loader.createdWakeEvent,
	}, nil
}

func (loader *fakeActivityLoader) loadProgram(userID uint32, _ sharedMaps) (activityProgram, error) {
	if loader.loadErr != nil {
		return nil, loader.loadErr
	}
	loader.loadedUserIDs = append(loader.loadedUserIDs, userID)
	program := &fakeActivityProgram{attachErr: loader.attachErr, interfaceIndex: loader.attachIndex}
	loader.programs = append(loader.programs, program)
	return program, nil
}

func (loader *fakeActivityLoader) resolveInterfaceIndex(_, _ string) (int, error) {
	return loader.resolveIndex, loader.resolveErr
}

func testUserIDRange() vm.UserIDRange { return vm.UserIDRange{Min: 100000, Max: 165535} }

func TestNewActivityMonitorCreatesTheSharedMapFromTheRange(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}

	if monitor.capacity != 65536 {
		t.Errorf("capacity = %d, want 65536", monitor.capacity)
	}
	if len(loader.capacities) != 1 || loader.capacities[0] != 65536 {
		t.Errorf("createMap capacities = %v, want [65536]", loader.capacities)
	}
}

func TestNewActivityMonitorRejectsAZeroCapacityRange(t *testing.T) {
	loader := &fakeActivityLoader{}
	// Max below Min gives a zero capacity.
	_, err := newMonitor(MonitorConfig{UserIDRange: vm.UserIDRange{Min: 10, Max: 9}}, loader)
	if err == nil {
		t.Fatal("want an error for a zero capacity")
	}
	if len(loader.capacities) != 0 {
		t.Error("createMap must not run for a zero capacity")
	}
}

func TestNewActivityMonitorRejectsACapacityOverTheLimit(t *testing.T) {
	loader := &fakeActivityLoader{}
	tooLarge := vm.UserIDRange{Min: 0, Max: maxActivityMapEntries}
	_, err := newMonitor(MonitorConfig{UserIDRange: tooLarge}, loader)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error = %v, want a limit error", err)
	}
}

func TestNewActivityMonitorWrapsTheCreateError(t *testing.T) {
	loader := &fakeActivityLoader{createErr: errors.New("kernel rejected map")}
	_, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err == nil || !strings.Contains(err.Error(), "shared maps") {
		t.Fatalf("error = %v, want the shared map context", err)
	}
}

func TestLoadProgramWrapsTheLoadErrorWithTheUserID(t *testing.T) {
	loader := &fakeActivityLoader{loadErr: errors.New("verifier rejected program")}
	monitor, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}

	_, err = monitor.loadProgram(100001)
	if err == nil || !strings.Contains(err.Error(), "100001") {
		t.Fatalf("error = %v, want the user ID context", err)
	}
}

func TestLoadProgramRecordsTheUserID(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := monitor.loadProgram(100002); err != nil {
		t.Fatal(err)
	}
	if len(loader.loadedUserIDs) != 1 || loader.loadedUserIDs[0] != 100002 {
		t.Errorf("loaded user IDs = %v, want [100002]", loader.loadedUserIDs)
	}
}

func TestCloseClosesTheSharedMap(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}

	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
	if !loader.createdMap.closed {
		t.Error("the shared map was not closed")
	}
}

func newTestMonitor(t *testing.T, loader *fakeActivityLoader) *Monitor {
	t.Helper()
	monitor, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	return monitor
}

func TestEnsureAttachmentAttachesAndStores(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)

	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	stored := monitor.attachments["vm-1"]
	if stored == nil || stored.interfaceIndex != 5 || stored.userID != 100001 {
		t.Fatalf("stored attachment = %+v", stored)
	}
	if len(loader.programs) != 1 || len(loader.programs[0].attachedPaths) != 1 {
		t.Fatalf("program was not attached once: %+v", loader.programs)
	}
	if got := loader.programs[0].attachedTaps[0]; got != "tap0" {
		t.Errorf("attached tap device = %q, want tap0", got)
	}
}

func TestEnsureAttachmentIsIdempotent(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	// tap0 still has index 5, so a repeat must not load a second program.
	loader.resolveIndex = 5
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}
	if len(loader.programs) != 1 {
		t.Errorf("loaded %d programs, want 1", len(loader.programs))
	}
}

func TestEnsureAttachmentReplacesWhenInterfaceIndexChanges(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	// tap0 was recreated with index 7, so the attachment must be replaced.
	loader.resolveIndex = 7
	loader.attachIndex = 7
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	if len(loader.programs) != 2 {
		t.Fatalf("loaded %d programs, want 2", len(loader.programs))
	}
	if !loader.programs[0].closed {
		t.Error("the replaced program was not closed")
	}
	if stored := monitor.attachments["vm-1"]; stored.interfaceIndex != 7 {
		t.Errorf("stored interface index = %d, want 7", stored.interfaceIndex)
	}
}

func TestEnsureAttachmentClosesTheProgramWhenAttachFails(t *testing.T) {
	loader := &fakeActivityLoader{attachErr: errors.New("egress attach failed")}
	monitor := newTestMonitor(t, loader)

	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err == nil {
		t.Fatal("want an attach error")
	}
	if len(loader.programs) != 1 || !loader.programs[0].closed {
		t.Error("the program must close when attach fails")
	}
	if monitor.attachments["vm-1"] != nil {
		t.Error("a failed attach must not store an attachment")
	}
}

func TestReleaseAttachmentClosesAndDeletesTheMapValue(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	if err := monitor.ReleaseAttachment("vm-1"); err != nil {
		t.Fatal(err)
	}
	if !loader.programs[0].closed {
		t.Error("the program was not closed")
	}
	if want := []uint32{100001}; !slices.Equal(loader.createdMap.deletedUserIDs, want) {
		t.Errorf("deleted user IDs = %v, want %v", loader.createdMap.deletedUserIDs, want)
	}
	if monitor.attachments["vm-1"] != nil {
		t.Error("the attachment was not removed")
	}
}

func TestArmNetworkWakeWritesArmed(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	if err := monitor.ArmNetworkWake(vm.NetworkActivityRequest{VirtualMachineID: "vm-1", UserID: 100001}); err != nil {
		t.Fatal(err)
	}
	if state := loader.createdWakeState.states[100001]; state != wakeArmed {
		t.Errorf("wake state = %d, want armed", state)
	}
}

func TestArmNetworkWakeRejectsAMissingAttachment(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor := newTestMonitor(t, loader)

	err := monitor.ArmNetworkWake(vm.NetworkActivityRequest{VirtualMachineID: "missing", UserID: 1})
	if !errors.Is(err, vm.ErrNotFound) {
		t.Fatalf("error = %v, want vm.ErrNotFound", err)
	}
}

func TestDisarmNetworkWakeWritesDisarmed(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}
	wakeRequest := vm.NetworkActivityRequest{VirtualMachineID: "vm-1", UserID: 100001}
	if err := monitor.ArmNetworkWake(wakeRequest); err != nil {
		t.Fatal(err)
	}

	// A repeat disarm must stay disarmed.
	for range 2 {
		if err := monitor.DisarmNetworkWake(wakeRequest); err != nil {
			t.Fatal(err)
		}
	}
	if state := loader.createdWakeState.states[100001]; state != wakeDisarmed {
		t.Errorf("wake state = %d, want disarmed", state)
	}
}

func TestDisarmNetworkWakeIsANoOpWithoutAttachment(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor := newTestMonitor(t, loader)

	if err := monitor.DisarmNetworkWake(vm.NetworkActivityRequest{VirtualMachineID: "missing", UserID: 1}); err != nil {
		t.Fatalf("disarm without an attachment returned %v", err)
	}
	if _, written := loader.createdWakeState.states[1]; written {
		t.Error("disarm without an attachment must not write a wake state")
	}
}

func TestReleaseAttachmentDeletesTheWakeState(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	if err := monitor.ReleaseAttachment("vm-1"); err != nil {
		t.Fatal(err)
	}
	if want := []uint32{100001}; !slices.Equal(loader.createdWakeState.deletedUserIDs, want) {
		t.Errorf("deleted wake user IDs = %v, want %v", loader.createdWakeState.deletedUserIDs, want)
	}
}

func TestHandleWakeEventDeliversAResolvedEvent(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	monitor.handleWakeEvent(100001, 42)
	select {
	case event := <-monitor.NetworkWakeEvents():
		if event.VirtualMachineID != "vm-1" || event.UserID != 100001 || event.PacketTime != 42 {
			t.Fatalf("event = %+v", event)
		}
	default:
		t.Fatal("no wake event was delivered")
	}
}

func TestHandleWakeEventDropsAnUnresolvedEvent(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor := newTestMonitor(t, loader)

	// User 999 has no attachment, so a released or stale event must be dropped.
	monitor.handleWakeEvent(999, 1)
	select {
	case <-monitor.NetworkWakeEvents():
		t.Fatal("an event for a user ID with no attachment must not be delivered")
	default:
	}
}

func TestHandleWakeEventRearmsWhenTheChannelIsFull(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	// Fill the channel, because there is no consumer.
	for i := 0; i < wakeEventChannelSize; i++ {
		monitor.handleWakeEvent(100001, uint64(i))
	}

	// A blocked event must rearm the VM.
	loader.createdWakeState.states = map[uint32]uint32{100001: wakeNotified}
	monitor.handleWakeEvent(100001, 999)
	if state := loader.createdWakeState.states[100001]; state != wakeArmed {
		t.Errorf("wake state = %d, want armed after a full channel", state)
	}
}

func TestNetworkWakeEventsClosesOnShutdown(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor, err := newMonitor(MonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}
	events := monitor.NetworkWakeEvents()

	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
	if _, open := <-events; open {
		t.Fatal("the wake event channel must be closed after shutdown")
	}
}

func TestReleaseAttachmentAcceptsAnAbsentAttachment(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor := newTestMonitor(t, loader)

	if err := monitor.ReleaseAttachment("missing"); err != nil {
		t.Fatalf("release of an absent attachment returned %v", err)
	}
}

func TestCloseReleasesEveryAttachment(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	for _, id := range []string{"vm-1", "vm-2"} {
		request := AttachmentRequest{VirtualMachineID: id, UserID: 100001, NamespacePath: "/run/netns/metal-" + id, TapName: "tap0"}
		if err := monitor.EnsureAttachment(request); err != nil {
			t.Fatal(err)
		}
	}

	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
	for _, program := range loader.programs {
		if !program.closed {
			t.Error("an attachment program was not closed")
		}
	}
	if !loader.createdMap.closed {
		t.Error("the shared map was not closed")
	}
}

func TestLastNetworkActivityReturnsBaselineBeforeTheFirstPacket(t *testing.T) {
	baseline := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	monitor, sharedMap := readMonitor(t, baseline, 1_000_000_000)
	sharedMap.lookupFound = false

	activity, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}
	if activity.HasBeenSeen {
		t.Error("a VM with no packet must report HasBeenSeen false")
	}
	if !activity.LastSeenAt.Equal(baseline) {
		t.Errorf("last seen = %v, want the baseline %v", activity.LastSeenAt, baseline)
	}
}

func TestLastNetworkActivityConvertsTheAgeToWallClock(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	monitor, sharedMap := readMonitor(t, now, 1_000_000_000)
	sharedMap.lookupFound = true
	sharedMap.lookupValue = 400_000_000 // 600 ms before the current monotonic time

	activity, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !activity.HasBeenSeen {
		t.Error("a VM with a packet must report HasBeenSeen true")
	}
	if want := now.Add(-600 * time.Millisecond); !activity.LastSeenAt.Equal(want) {
		t.Errorf("last seen = %v, want %v", activity.LastSeenAt, want)
	}
}

func TestLastNetworkActivityLaterPacketIsMoreRecent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	monitor, sharedMap := readMonitor(t, now, 10_000_000_000)
	sharedMap.lookupFound = true

	sharedMap.lookupValue = 2_000_000_000 // 8 s old
	older, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}
	sharedMap.lookupValue = 6_000_000_000 // 4 s old
	newer, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !newer.LastSeenAt.After(older.LastSeenAt) {
		t.Errorf("later packet %v is not after earlier packet %v", newer.LastSeenAt, older.LastSeenAt)
	}
}

func TestLastNetworkActivityUsesMonotonicAgeNotWallClock(t *testing.T) {
	// A backward wall-clock jump must not make activity older.
	monitor, sharedMap := readMonitor(t, time.Time{}, 5_000_000_000)
	sharedMap.lookupFound = true
	sharedMap.lookupValue = 3_000_000_000 // 2 s of age

	forward := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	monitor.wallClock = func() time.Time { return forward }
	forwardActivity, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}

	backward := forward.Add(-time.Hour)
	monitor.wallClock = func() time.Time { return backward }
	backwardActivity, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}

	if forward.Sub(forwardActivity.LastSeenAt) != backward.Sub(backwardActivity.LastSeenAt) {
		t.Error("a backward wall clock changed the computed age")
	}
}

func TestLastNetworkActivityClampsAFutureMapValue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	monitor, sharedMap := readMonitor(t, now, 1_000_000_000)
	sharedMap.lookupFound = true
	sharedMap.lookupValue = 2_000_000_000 // ahead of the current monotonic time

	activity, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !activity.LastSeenAt.Equal(now) {
		t.Errorf("last seen = %v, want the current time %v", activity.LastSeenAt, now)
	}
}

func TestLastNetworkActivityReturnsNotFoundWithoutAnAttachment(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor := newTestMonitor(t, loader)

	_, err := monitor.LastNetworkActivity(context.Background(), vm.NetworkActivityRequest{VirtualMachineID: "missing", UserID: 1})
	if !errors.Is(err, vm.ErrNotFound) {
		t.Fatalf("error = %v, want vm.ErrNotFound", err)
	}
}

func TestLastNetworkActivityReturnsNotFoundAfterRelease(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}
	if err := monitor.ReleaseAttachment("vm-1"); err != nil {
		t.Fatal(err)
	}

	_, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if !errors.Is(err, vm.ErrNotFound) {
		t.Fatalf("error = %v, want vm.ErrNotFound after release", err)
	}
}

func TestLastNetworkActivityWrapsTheLookupErrorWithContext(t *testing.T) {
	monitor, sharedMap := readMonitor(t, time.Now(), 1)
	sharedMap.lookupErr = errors.New("map read failed")

	_, err := monitor.LastNetworkActivity(context.Background(), activityRequest)
	if err == nil || !strings.Contains(err.Error(), "vm-1") || !strings.Contains(err.Error(), "100001") {
		t.Fatalf("error = %v, want the VM and user ID context", err)
	}
}

func TestConcurrentActivityReadsAndReplacement(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1", TapName: "tap0"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}
	loader.createdMap.lookupFound = true
	loader.createdMap.lookupValue = 1

	var group sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < 200; i++ {
				_, _ = monitor.LastNetworkActivity(context.Background(), activityRequest)
			}
		}()
	}
	// One replacer, so the fake loader has a single writer.
	group.Add(1)
	go func() {
		defer group.Done()
		for i := 0; i < 100; i++ {
			loader.resolveIndex = 5 + i%2 // alternate to force some replacements
			loader.attachIndex = loader.resolveIndex
			_ = monitor.EnsureAttachment(request)
		}
	}()
	group.Wait()
}
