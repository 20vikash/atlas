package network

import (
	"errors"
	"fmt"

	"github.com/frappe/atlas/metal/internal/vm"
)

// maxActivityMapEntries caps the shared activity map. It stops a wrong user ID
// range from asking the kernel for an unreasonable map.
const maxActivityMapEntries = 1 << 20

// ActivityMonitor loads and owns the eBPF programs that record VM packet
// activity. It keeps one shared map keyed by VM user ID and loads one program
// instance for each VM. The metald process owns the monitor and closes it.
type ActivityMonitor struct {
	loader   activityLoader
	capacity uint32
	shared   activityMap
}

// ActivityMonitorConfig configures the shared activity map.
type ActivityMonitorConfig struct {
	// UserIDRange sets the shared map capacity. Every VM user ID must fit.
	UserIDRange vm.UserIDRange
}

// capacity returns the number of user IDs the shared map must hold.
func (config ActivityMonitorConfig) capacity() uint32 {
	if config.UserIDRange.Max < config.UserIDRange.Min {
		return 0
	}
	return config.UserIDRange.Max - config.UserIDRange.Min + 1
}

// activityMap is the shared BPF map keyed by VM user ID. Later commits add read
// and delete methods. The monitor owns the handle and closes it.
type activityMap interface {
	Close() error
}

// activityProgram is one loaded program instance for one VM. The monitor owns
// the handle and closes it, which also closes its TCX links.
type activityProgram interface {
	// attach hooks tap0 ingress and egress inside the VM network namespace and
	// returns the resolved tap0 interface index.
	attach(namespacePath string) (interfaceIndex int, err error)
	Close() error
}

// activityLoader creates the kernel objects the monitor needs. A fake replaces
// it in unit tests, so the tests do not need root.
type activityLoader interface {
	createMap(capacity uint32) (activityMap, error)
	loadProgram(userID uint32, shared activityMap) (activityProgram, error)
}

// NewActivityMonitor creates the shared activity map and returns the monitor.
func NewActivityMonitor(config ActivityMonitorConfig) (*ActivityMonitor, error) {
	return newActivityMonitor(config, bpfActivityLoader{})
}

// newActivityMonitor builds the monitor with an injected loader for tests.
func newActivityMonitor(config ActivityMonitorConfig, loader activityLoader) (*ActivityMonitor, error) {
	capacity := config.capacity()
	if capacity == 0 {
		return nil, errors.New("activity map capacity is zero")
	}
	if capacity > maxActivityMapEntries {
		return nil, fmt.Errorf("activity map capacity %d is over the limit %d", capacity, maxActivityMapEntries)
	}

	shared, err := loader.createMap(capacity)
	if err != nil {
		return nil, fmt.Errorf("create shared activity map: %w", err)
	}

	return &ActivityMonitor{loader: loader, capacity: capacity, shared: shared}, nil
}

// loadProgram loads one program instance for a VM user ID with the shared map.
func (monitor *ActivityMonitor) loadProgram(userID uint32) (activityProgram, error) {
	program, err := monitor.loader.loadProgram(userID, monitor.shared)
	if err != nil {
		return nil, fmt.Errorf("load activity program for user %d: %w", userID, err)
	}
	return program, nil
}

// Close releases the shared activity map. Later commits also release the loaded
// per-VM programs and their links.
func (monitor *ActivityMonitor) Close() error {
	return monitor.shared.Close()
}
