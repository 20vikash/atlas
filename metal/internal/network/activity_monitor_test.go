package network

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/vm"
)

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

// fakeActivityProgram records how the monitor attaches and closes the program.
type fakeActivityProgram struct {
	attachErr      error
	attachedPaths  []string
	interfaceIndex int
	closed         bool
}

func (p *fakeActivityProgram) attach(namespacePath string) (int, error) {
	if p.attachErr != nil {
		return 0, p.attachErr
	}
	p.attachedPaths = append(p.attachedPaths, namespacePath)
	return p.interfaceIndex, nil
}

func (p *fakeActivityProgram) Close() error {
	p.closed = true
	return nil
}

// fakeActivityLoader stands in for the kernel so tests need no root.
type fakeActivityLoader struct {
	createErr     error
	loadErr       error
	attachErr     error
	resolveErr    error
	resolveIndex  int
	attachIndex   int
	createdMap    *fakeActivityMap
	capacities    []uint32
	loadedUserIDs []uint32
	programs      []*fakeActivityProgram
}

func (loader *fakeActivityLoader) createMap(capacity uint32) (activityMap, error) {
	if loader.createErr != nil {
		return nil, loader.createErr
	}
	loader.capacities = append(loader.capacities, capacity)
	loader.createdMap = &fakeActivityMap{}
	return loader.createdMap, nil
}

func (loader *fakeActivityLoader) loadProgram(userID uint32, _ activityMap) (activityProgram, error) {
	if loader.loadErr != nil {
		return nil, loader.loadErr
	}
	loader.loadedUserIDs = append(loader.loadedUserIDs, userID)
	program := &fakeActivityProgram{attachErr: loader.attachErr, interfaceIndex: loader.attachIndex}
	loader.programs = append(loader.programs, program)
	return program, nil
}

func (loader *fakeActivityLoader) resolveInterfaceIndex(_ string) (int, error) {
	return loader.resolveIndex, loader.resolveErr
}

func testUserIDRange() vm.UserIDRange { return vm.UserIDRange{Min: 100000, Max: 165535} }

func TestNewActivityMonitorCreatesTheSharedMapFromTheRange(t *testing.T) {
	loader := &fakeActivityLoader{}
	monitor, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: testUserIDRange()}, loader)
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
	_, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: vm.UserIDRange{Min: 10, Max: 9}}, loader)
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
	_, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: tooLarge}, loader)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error = %v, want a limit error", err)
	}
}

func TestNewActivityMonitorWrapsTheCreateError(t *testing.T) {
	loader := &fakeActivityLoader{createErr: errors.New("kernel rejected map")}
	_, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err == nil || !strings.Contains(err.Error(), "shared activity map") {
		t.Fatalf("error = %v, want the shared map context", err)
	}
}

func TestLoadProgramWrapsTheLoadErrorWithTheUserID(t *testing.T) {
	loader := &fakeActivityLoader{loadErr: errors.New("verifier rejected program")}
	monitor, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: testUserIDRange()}, loader)
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
	monitor, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: testUserIDRange()}, loader)
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
	monitor, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: testUserIDRange()}, loader)
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

func newTestMonitor(t *testing.T, loader *fakeActivityLoader) *ActivityMonitor {
	t.Helper()
	monitor, err := newActivityMonitor(ActivityMonitorConfig{UserIDRange: testUserIDRange()}, loader)
	if err != nil {
		t.Fatal(err)
	}
	return monitor
}

func TestEnsureAttachmentAttachesAndStores(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)

	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1"}
	if err := monitor.EnsureAttachment(request); err != nil {
		t.Fatal(err)
	}

	stored := monitor.attachments["vm-1"]
	if stored == nil || stored.interfaceIndex != 5 || stored.userID != 100001 {
		t.Fatalf("stored attachment = %+v", stored)
	}
	if len(loader.programs) != 1 || len(loader.programs[0].attachedPaths) != 1 {
		t.Errorf("program was not attached once: %+v", loader.programs)
	}
}

func TestEnsureAttachmentIsIdempotent(t *testing.T) {
	loader := &fakeActivityLoader{attachIndex: 5}
	monitor := newTestMonitor(t, loader)
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1"}
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
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1"}
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

	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1"}
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
	request := AttachmentRequest{VirtualMachineID: "vm-1", UserID: 100001, NamespacePath: "/run/netns/metal-vm-1"}
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
		request := AttachmentRequest{VirtualMachineID: id, UserID: 100001, NamespacePath: "/run/netns/metal-" + id}
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
