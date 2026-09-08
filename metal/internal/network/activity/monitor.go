// Package activity tracks host-to-guest traffic with eBPF.
package activity

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

// maxActivityMapEntries limits memory use from an invalid user ID range.
const maxActivityMapEntries = 1 << 20

// These wake states match the eBPF program.
const (
	wakeDisarmed uint32 = 0
	wakeArmed    uint32 = 1
	wakeNotified uint32 = 2
)

// Monitor owns the eBPF activity and wake resources for all VMs.
type Monitor struct {
	loader   activityLoader
	capacity uint32
	shared   sharedMaps
	logger   *slog.Logger

	// Tests replace these clocks.
	wallClock      func() time.Time
	monotonicClock func() (uint64, error)

	// Close stops the reader before closing maps.
	wakeReader wakeEventReader
	wakeEvents chan vm.NetworkWakeEvent
	workerDone chan struct{}
	closeOnce  sync.Once

	mu          sync.Mutex
	attachments map[string]*attachment
	// Release removes state before user ID reuse.
	byUserID map[uint32]string
}

// wakeEventChannelSize limits buffered wake events. A full channel rearms the VM.
const wakeEventChannelSize = 128

// attachment owns the activity resources of one VM.
type attachment struct {
	userID         uint32
	namespacePath  string
	interfaceIndex int
	program        activityProgram
	// baseline is the attachment time before the first packet.
	baseline time.Time
}

// Check the manager interfaces at compile time.
var (
	_ vm.NetworkActivityMonitor = (*Monitor)(nil)
	_ vm.NetworkWakeMonitor     = (*Monitor)(nil)
)

// AttachmentRequest identifies one VM activity attachment.
type AttachmentRequest struct {
	VirtualMachineID string
	UserID           uint32
	NamespacePath    string
	TapName          string
}

// MonitorConfig configures the shared activity map.
type MonitorConfig struct {
	// UserIDRange sets the shared map capacity.
	UserIDRange vm.UserIDRange
	// Logger is optional.
	Logger *slog.Logger
}

// capacity returns the number of user IDs the shared map must hold.
func (config MonitorConfig) capacity() uint32 {
	if config.UserIDRange.Max < config.UserIDRange.Min {
		return 0
	}
	return config.UserIDRange.Max - config.UserIDRange.Min + 1
}

// sharedMaps holds the kernel maps shared by all VM programs.
type sharedMaps struct {
	activity   activityMap
	wakeState  wakeStateMap
	wakeEvents wakeEventMap
}

// activityMap stores packet times by VM user ID.
type activityMap interface {
	// lookup returns found=false before the first packet.
	lookup(userID uint32) (nanoseconds uint64, found bool, err error)
	// delete removes one VM user ID.
	delete(userID uint32) error
	Close() error
}

// wakeStateMap stores wake states by VM user ID.
type wakeStateMap interface {
	// setState writes one VM wake state.
	setState(userID, state uint32) error
	// deleteState removes one VM wake state.
	deleteState(userID uint32) error
	Close() error
}

// wakeEventMap carries wake events from eBPF.
type wakeEventMap interface {
	// newReader opens a ring reader.
	newReader() (wakeEventReader, error)
	Close() error
}

// wakeEventReader reads wake events. Close unblocks a pending read.
type wakeEventReader interface {
	// read returns the next wake event.
	read() (userID uint32, packetTimeNanoseconds uint64, err error)
	Close() error
}

// activityProgram owns one VM program and its TCX link.
type activityProgram interface {
	// attach hooks tap egress and returns its interface index.
	attach(namespacePath, tapName string) (interfaceIndex int, err error)
	Close() error
}

// activityLoader creates the required kernel objects.
type activityLoader interface {
	createSharedMaps(capacity uint32) (sharedMaps, error)
	loadProgram(userID uint32, shared sharedMaps) (activityProgram, error)
	// resolveInterfaceIndex reads the tap device index in the namespace.
	resolveInterfaceIndex(namespacePath, tapName string) (interfaceIndex int, err error)
}

// NewMonitor creates the shared activity map and returns the monitor.
func NewMonitor(config MonitorConfig) (*Monitor, error) {
	return newMonitor(config, bpfActivityLoader{})
}

// newMonitor builds the monitor with an injected loader for tests.
func newMonitor(config MonitorConfig, loader activityLoader) (*Monitor, error) {
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

	reader, err := shared.wakeEvents.newReader()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open wake event reader: %w", err),
			shared.activity.Close(), shared.wakeState.Close(), shared.wakeEvents.Close())
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	monitor := &Monitor{
		loader:         loader,
		capacity:       capacity,
		shared:         shared,
		logger:         logger,
		wallClock:      time.Now,
		monotonicClock: readMonotonicNanoseconds,
		wakeReader:     reader,
		wakeEvents:     make(chan vm.NetworkWakeEvent, wakeEventChannelSize),
		workerDone:     make(chan struct{}),
		attachments:    map[string]*attachment{},
		byUserID:       map[uint32]string{},
	}
	go monitor.runWakeReader()
	return monitor, nil
}

// runWakeReader forwards events until the reader closes.
func (monitor *Monitor) runWakeReader() {
	defer close(monitor.workerDone)
	defer close(monitor.wakeEvents)
	for {
		userID, packetTime, err := monitor.wakeReader.read()
		if err != nil {
			return
		}
		monitor.handleWakeEvent(userID, packetTime)
	}
}

// handleWakeEvent drops stale events and rearms when the channel is full.
func (monitor *Monitor) handleWakeEvent(userID uint32, packetTime uint64) {
	monitor.mu.Lock()
	virtualMachineID, resolved := monitor.byUserID[userID]
	monitor.mu.Unlock()
	if !resolved {
		return
	}

	event := vm.NetworkWakeEvent{VirtualMachineID: virtualMachineID, UserID: userID, PacketTime: packetTime}
	select {
	case monitor.wakeEvents <- event:
	default:
		if err := monitor.shared.wakeState.setState(userID, wakeArmed); err != nil {
			monitor.logger.Warn("rearm after a full wake channel failed",
				"virtual_machine_id", virtualMachineID, "user_id", userID, "error", err)
		}
	}
}

// NetworkWakeEvents returns the wake event channel.
func (monitor *Monitor) NetworkWakeEvents() <-chan vm.NetworkWakeEvent {
	return monitor.wakeEvents
}

// readMonotonicNanoseconds reads the clock used by bpf_ktime_get_ns.
func readMonotonicNanoseconds() (uint64, error) {
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		return 0, err
	}
	return uint64(now.Sec)*uint64(time.Second/time.Nanosecond) + uint64(now.Nsec), nil
}

// loadProgram loads one program instance for a VM user ID with the shared map.
func (monitor *Monitor) loadProgram(userID uint32) (activityProgram, error) {
	program, err := monitor.loader.loadProgram(userID, monitor.shared)
	if err != nil {
		return nil, fmt.Errorf("load activity program for user %d: %w", userID, err)
	}
	return program, nil
}

// EnsureAttachment attaches activity tracking to a VM tap device.
func (monitor *Monitor) EnsureAttachment(request AttachmentRequest) error {
	monitor.mu.Lock()
	existing := monitor.attachments[request.VirtualMachineID]
	monitor.mu.Unlock()

	if existing != nil && existing.userID == request.UserID && existing.namespacePath == request.NamespacePath {
		index, err := monitor.loader.resolveInterfaceIndex(request.NamespacePath, request.TapName)
		if err != nil {
			return fmt.Errorf("resolve %s for VM %s: %w", request.TapName, request.VirtualMachineID, err)
		}
		if index == existing.interfaceIndex {
			return nil
		}
	}

	program, err := monitor.loadProgram(request.UserID)
	if err != nil {
		return fmt.Errorf("VM %s: %w", request.VirtualMachineID, err)
	}
	interfaceIndex, err := program.attach(request.NamespacePath, request.TapName)
	if err != nil {
		return errors.Join(fmt.Errorf("attach activity program for VM %s: %w", request.VirtualMachineID, err), program.Close())
	}

	monitor.mu.Lock()
	replaced := monitor.attachments[request.VirtualMachineID]
	if replaced != nil && replaced.userID != request.UserID {
		delete(monitor.byUserID, replaced.userID)
	}
	monitor.attachments[request.VirtualMachineID] = &attachment{
		userID:         request.UserID,
		namespacePath:  request.NamespacePath,
		interfaceIndex: interfaceIndex,
		program:        program,
		baseline:       monitor.wallClock(),
	}
	monitor.byUserID[request.UserID] = request.VirtualMachineID
	monitor.mu.Unlock()

	if replaced != nil {
		return replaced.program.Close()
	}
	return nil
}

// ReleaseAttachment releases the program, link, activity, and wake state for one VM.
func (monitor *Monitor) ReleaseAttachment(virtualMachineID string) error {
	monitor.mu.Lock()
	released := monitor.attachments[virtualMachineID]
	delete(monitor.attachments, virtualMachineID)
	if released != nil {
		delete(monitor.byUserID, released.userID)
	}
	monitor.mu.Unlock()

	if released == nil {
		return nil
	}

	closeError := released.program.Close()
	deleteActivityError := monitor.shared.activity.delete(released.userID)
	deleteWakeError := monitor.shared.wakeState.deleteState(released.userID)
	return errors.Join(closeError, deleteActivityError, deleteWakeError)
}

// ArmNetworkWake arms one host-to-guest wake event.
func (monitor *Monitor) ArmNetworkWake(request vm.NetworkActivityRequest) error {
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

// DisarmNetworkWake stops wake events for the VM.
func (monitor *Monitor) DisarmNetworkWake(request vm.NetworkActivityRequest) error {
	monitor.mu.Lock()
	current := monitor.attachments[request.VirtualMachineID]
	monitor.mu.Unlock()
	if current == nil || current.userID != request.UserID {
		return nil
	}

	if err := monitor.shared.wakeState.setState(request.UserID, wakeDisarmed); err != nil {
		return fmt.Errorf("disarm wake for VM %s user %d: %w", request.VirtualMachineID, request.UserID, err)
	}
	return nil
}

// LastNetworkActivity returns the last packet time for one VM.
func (monitor *Monitor) LastNetworkActivity(_ context.Context, request vm.NetworkActivityRequest) (vm.NetworkActivity, error) {
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
		// Clamp future activity to now.
		monitor.logger.Warn("activity time is in the future",
			"virtual_machine_id", request.VirtualMachineID, "user_id", request.UserID)
	}

	lastSeenAt := monitor.wallClock().UTC().Add(-time.Duration(ageNanoseconds))
	return vm.NetworkActivity{LastSeenAt: lastSeenAt, HasBeenSeen: true, LastPacketMonotonicNanoseconds: value}, nil
}

// Close stops event delivery and releases all monitor resources.
func (monitor *Monitor) Close() error {
	var closeErrors []error
	monitor.closeOnce.Do(func() {
		// Stop the reader before closing maps.
		closeErrors = append(closeErrors, monitor.wakeReader.Close())
		<-monitor.workerDone

		monitor.mu.Lock()
		attachments := monitor.attachments
		monitor.attachments = map[string]*attachment{}
		monitor.byUserID = map[uint32]string{}
		monitor.mu.Unlock()

		for _, current := range attachments {
			closeErrors = append(closeErrors, current.program.Close())
		}
		closeErrors = append(closeErrors,
			monitor.shared.activity.Close(),
			monitor.shared.wakeState.Close(),
			monitor.shared.wakeEvents.Close())
	})
	return errors.Join(closeErrors...)
}
