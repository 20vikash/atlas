package network

import (
	"errors"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/vm"
)

// fakeActivityMap records whether the monitor closed the shared map.
type fakeActivityMap struct {
	closed bool
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
	createdMap    *fakeActivityMap
	capacities    []uint32
	loadedUserIDs []uint32
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
	return &fakeActivityProgram{}, nil
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
