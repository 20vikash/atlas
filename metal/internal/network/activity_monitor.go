package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/frappe/atlas/metal/internal/vm"
)

// maxActivityMapEntries caps the shared activity map. It stops a wrong user ID
// range from asking the kernel for an unreasonable map.
const maxActivityMapEntries = 1 << 20

// Wake states stored in the shared wake state map. They match the eBPF program.
// A missing key means disarmed.
const (
	wakeDisarmed uint32 = 0
	wakeArmed    uint32 = 1
	wakeNotified uint32 = 2
)

// ActivityMonitor loads and owns the eBPF programs that record VM packet
// activity. It keeps one shared map keyed by VM user ID and one program
// instance for each VM. The metald process owns the monitor and closes it.
type ActivityMonitor struct {
	loader   activityLoader
	capacity uint32
	shared   sharedMaps
	logger   *slog.Logger

	// wallClock and monotonicClock are seams. Tests replace them.
	wallClock      func() time.Time
	monotonicClock func() (uint64, error)

	mu          sync.Mutex
	attachments map[string]*attachment
}

// attachment owns the activity resources of one VM.
type attachment struct {
	userID         uint32
	namespacePath  string
	interfaceIndex int
	program        activityProgram
	// baseline is the wall-clock attachment time. It is the safe last-seen value
	// before the first packet.
	baseline time.Time
}

// Compile-time proof that the monitor satisfies the manager seam.
var _ vm.NetworkActivityMonitor = (*ActivityMonitor)(nil)

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
	// Logger receives local warnings, such as a map time in the future. It
	// defaults to slog.Default when nil.
	Logger *slog.Logger
}

// capacity returns the number of user IDs the shared map must hold.
func (config ActivityMonitorConfig) capacity() uint32 {
	if config.UserIDRange.Max < config.UserIDRange.Min {
		return 0
	}
	return config.UserIDRange.Max - config.UserIDRange.Min + 1
}

// sharedMaps holds the three kernel maps that every VM program shares. The
// monitor owns them and closes them.
type sharedMaps struct {
	activity   activityMap
	wakeState  wakeStateMap
	wakeEvents wakeEventMap
}

// activityMap is the shared BPF map keyed by VM user ID. The monitor owns the
// handle and closes it.
type activityMap interface {
	// lookup returns the last monotonic packet time for one VM user ID. found is
	// false when the VM has no packet entry yet.
	lookup(userID uint32) (nanoseconds uint64, found bool, err error)
	// delete removes the activity value for one released VM user ID.
	delete(userID uint32) error
	Close() error
}

// wakeStateMap is the shared BPF hash that holds the wake state of each VM user
// ID. The monitor owns the handle and closes it.
type wakeStateMap interface {
	// setState writes one wake state for a VM user ID.
	setState(userID, state uint32) error
	// deleteState removes the wake state for one released VM user ID.
	deleteState(userID uint32) error
	Close() error
}

// wakeEventMap is the shared BPF ring buffer that carries wake events. The
// monitor owns the handle and closes it.
type wakeEventMap interface {
	// newReader opens one ring reader on the shared map.
	newReader() (wakeEventReader, error)
	Close() error
}

// wakeEventReader reads decoded wake events from the shared ring buffer. Close
// unblocks a pending read.
type wakeEventReader interface {
	// read returns the next event user ID and monotonic packet time.
	read() (userID uint32, packetTimeNanoseconds uint64, err error)
	Close() error
}

// activityProgram is one loaded program instance for one VM. The monitor owns
// the handle and closes it, which also closes its TCX links.
type activityProgram interface {
	// attach hooks the tap0 egress path inside the VM network namespace and
	// returns the resolved tap0 interface index.
	attach(namespacePath string) (interfaceIndex int, err error)
	Close() error
}

// activityLoader creates the kernel objects the monitor needs. A fake replaces
// it in unit tests, so the tests do not need root.
type activityLoader interface {
	createSharedMaps(capacity uint32) (sharedMaps, error)
	loadProgram(userID uint32, shared sharedMaps) (activityProgram, error)
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

	shared, err := loader.createSharedMaps(capacity)
	if err != nil {
		return nil, fmt.Errorf("create shared maps: %w", err)
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &ActivityMonitor{
		loader:         loader,
		capacity:       capacity,
		shared:         shared,
		logger:         logger,
		wallClock:      time.Now,
		monotonicClock: readMonotonicNanoseconds,
		attachments:    map[string]*attachment{},
	}, nil
}

// readMonotonicNanoseconds reads CLOCK_MONOTONIC, the same clock as the eBPF
// bpf_ktime_get_ns helper.
func readMonotonicNanoseconds() (uint64, error) {
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		return 0, err
	}
	return uint64(now.Sec)*uint64(time.Second/time.Nanosecond) + uint64(now.Nsec), nil
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
		baseline:       monitor.wallClock(),
	}
	monitor.mu.Unlock()

	if replaced != nil {
		return replaced.program.Close()
	}
	return nil
}

// ReleaseAttachment closes the links and program of one VM, then removes its
// activity value and wake state. It clears the wake state before the user ID can
// be reused, so a stale event cannot wake a new VM. It accepts a VM that has no
// attachment.
func (monitor *ActivityMonitor) ReleaseAttachment(virtualMachineID string) error {
	monitor.mu.Lock()
	released := monitor.attachments[virtualMachineID]
	delete(monitor.attachments, virtualMachineID)
	monitor.mu.Unlock()

	if released == nil {
		return nil
	}

	closeError := released.program.Close()
	deleteActivityError := monitor.shared.activity.delete(released.userID)
	deleteWakeError := monitor.shared.wakeState.deleteState(released.userID)
	return errors.Join(closeError, deleteActivityError, deleteWakeError)
}

// ArmNetworkWake makes the next host-to-guest packet for the VM produce one wake
// event. It needs an existing activity attachment with the matching user ID.
func (monitor *ActivityMonitor) ArmNetworkWake(request vm.NetworkActivityRequest) error {
	monitor.mu.Lock()
	current := monitor.attachments[request.VirtualMachineID]
	monitor.mu.Unlock()
	if current == nil || current.userID != request.UserID {
		return fmt.Errorf("arm wake for VM %s: %w", request.VirtualMachineID, vm.ErrNotFound)
	}

	if err := monitor.shared.wakeState.setState(request.UserID, wakeArmed); err != nil {
		return fmt.Errorf("arm wake for VM %s user %d: %w", request.VirtualMachineID, request.UserID, err)
	}
	return nil
}

// LastNetworkActivity returns the last packet time for one VM. It returns the
// attachment baseline with HasBeenSeen false before the first packet, and
// vm.ErrNotFound when the VM has no attachment. It reads CLOCK_MONOTONIC right
// after the map lookup and converts the age to a wall-clock time.
func (monitor *ActivityMonitor) LastNetworkActivity(_ context.Context, request vm.NetworkActivityRequest) (vm.NetworkActivity, error) {
	monitor.mu.Lock()
	current := monitor.attachments[request.VirtualMachineID]
	monitor.mu.Unlock()
	if current == nil {
		return vm.NetworkActivity{}, fmt.Errorf("activity for VM %s: %w", request.VirtualMachineID, vm.ErrNotFound)
	}

	value, found, err := monitor.shared.activity.lookup(request.UserID)
	if err != nil {
		return vm.NetworkActivity{}, fmt.Errorf("read activity for VM %s user %d: %w", request.VirtualMachineID, request.UserID, err)
	}
	if !found {
		return vm.NetworkActivity{LastSeenAt: current.baseline}, nil
	}

	nowMonotonic, err := monitor.monotonicClock()
	if err != nil {
		return vm.NetworkActivity{}, fmt.Errorf("read monotonic clock for VM %s: %w", request.VirtualMachineID, err)
	}

	var ageNanoseconds uint64
	if nowMonotonic >= value {
		ageNanoseconds = nowMonotonic - value
	} else {
		// A map time ahead of the clock must never read as fresh activity that
		// could delay sleep. Clamp it to zero age and warn.
		monitor.logger.Warn("activity time is in the future",
			"virtual_machine_id", request.VirtualMachineID, "user_id", request.UserID)
	}

	lastSeenAt := monitor.wallClock().UTC().Add(-time.Duration(ageNanoseconds))
	return vm.NetworkActivity{LastSeenAt: lastSeenAt, HasBeenSeen: true}, nil
}

// Close releases every attachment and the three shared maps. It is the one
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
	closeErrors = append(closeErrors,
		monitor.shared.activity.Close(),
		monitor.shared.wakeState.Close(),
		monitor.shared.wakeEvents.Close())
	return errors.Join(closeErrors...)
}
