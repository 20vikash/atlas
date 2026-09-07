package network

import (
	"errors"
	"fmt"
	"sync"

	"github.com/frappe/atlas/metal/internal/vm"
)

// maxActivityMapEntries caps the shared activity map. It stops a wrong user ID
// range from asking the kernel for an unreasonable map.
const maxActivityMapEntries = 1 << 20

// ActivityMonitor loads and owns the eBPF programs that record VM packet
// activity. It keeps one shared map keyed by VM user ID and one program
// instance for each VM. The metald process owns the monitor and closes it.
type ActivityMonitor struct {
	loader   activityLoader
	capacity uint32
	shared   activityMap

	mu          sync.Mutex
	attachments map[string]*attachment
}

// attachment owns the activity resources of one VM.
type attachment struct {
	userID         uint32
	namespacePath  string
	interfaceIndex int
	program        activityProgram
}

// AttachmentRequest identifies one VM activity attachment.
type AttachmentRequest struct {
	VirtualMachineID string
	UserID           uint32
	NamespacePath    string
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

// activityMap is the shared BPF map keyed by VM user ID. Later commits add a
// read method. The monitor owns the handle and closes it.
type activityMap interface {
	// delete removes the activity value for one released VM user ID.
	delete(userID uint32) error
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
	// resolveInterfaceIndex reads the current tap0 index inside the namespace.
	resolveInterfaceIndex(namespacePath string) (interfaceIndex int, err error)
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

	return &ActivityMonitor{
		loader:      loader,
		capacity:    capacity,
		shared:      shared,
		attachments: map[string]*attachment{},
	}, nil
}

// loadProgram loads one program instance for a VM user ID with the shared map.
func (monitor *ActivityMonitor) loadProgram(userID uint32) (activityProgram, error) {
	program, err := monitor.loader.loadProgram(userID, monitor.shared)
	if err != nil {
		return nil, fmt.Errorf("load activity program for user %d: %w", userID, err)
	}
	return program, nil
}

// EnsureAttachment makes one VM have an activity attachment for its tap0. It is
// idempotent for the same VM, user ID, namespace, and interface index. It
// replaces the attachment when tap0 was recreated with a new index. Blocking
// kernel calls run outside the lock.
func (monitor *ActivityMonitor) EnsureAttachment(request AttachmentRequest) error {
	monitor.mu.Lock()
	existing := monitor.attachments[request.VirtualMachineID]
	monitor.mu.Unlock()

	if existing != nil && existing.userID == request.UserID && existing.namespacePath == request.NamespacePath {
		index, err := monitor.loader.resolveInterfaceIndex(request.NamespacePath)
		if err != nil {
			return fmt.Errorf("resolve %s for VM %s: %w", tapName, request.VirtualMachineID, err)
		}
		if index == existing.interfaceIndex {
			return nil
		}
	}

	program, err := monitor.loadProgram(request.UserID)
	if err != nil {
		return fmt.Errorf("VM %s: %w", request.VirtualMachineID, err)
	}
	interfaceIndex, err := program.attach(request.NamespacePath)
	if err != nil {
		return errors.Join(fmt.Errorf("attach activity program for VM %s: %w", request.VirtualMachineID, err), program.Close())
	}

	monitor.mu.Lock()
	replaced := monitor.attachments[request.VirtualMachineID]
	monitor.attachments[request.VirtualMachineID] = &attachment{
		userID:         request.UserID,
		namespacePath:  request.NamespacePath,
		interfaceIndex: interfaceIndex,
		program:        program,
	}
	monitor.mu.Unlock()

	if replaced != nil {
		return replaced.program.Close()
	}
	return nil
}

// ReleaseAttachment closes the links and program of one VM, then removes its
// activity value. It accepts a VM that has no attachment.
func (monitor *ActivityMonitor) ReleaseAttachment(virtualMachineID string) error {
	monitor.mu.Lock()
	released := monitor.attachments[virtualMachineID]
	delete(monitor.attachments, virtualMachineID)
	monitor.mu.Unlock()

	if released == nil {
		return nil
	}

	closeError := released.program.Close()
	deleteError := monitor.shared.delete(released.userID)
	return errors.Join(closeError, deleteError)
}

// Close releases every attachment and the shared activity map. It is the one
// owner of the monitor shutdown.
func (monitor *ActivityMonitor) Close() error {
	monitor.mu.Lock()
	attachments := monitor.attachments
	monitor.attachments = map[string]*attachment{}
	monitor.mu.Unlock()

	var closeErrors []error
	for _, current := range attachments {
		closeErrors = append(closeErrors, current.program.Close())
	}
	closeErrors = append(closeErrors, monitor.shared.Close())
	return errors.Join(closeErrors...)
}
