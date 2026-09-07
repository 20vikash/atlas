// Package firecracker controls Firecracker virtual machines.
package firecracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

// maxConcurrentSSHSessions limits host-wide SSH console sessions.
const maxConcurrentSSHSessions = 32

// virtualMachineStorage prepares and releases the disk of one VM.
type virtualMachineStorage interface {
	PrepareBoot(ctx context.Context, request storage.VirtualMachineStorageRequest) (storage.BootConfiguration, error)
	PrepareRootFileSystem(ctx context.Context, request storage.VirtualMachineStorageRequest) error
	Release(ctx context.Context, virtualMachineID string) error
}

// imageStore supplies boot images and their warm artifacts.
type imageStore interface {
	EnsureImage(ctx context.Context, image vm.Image) error
	WarmImage(
		ctx context.Context,
		image vm.Image,
		configuration vm.MemorySnapshotConfiguration,
		firecrackerCompatibility string,
	) (storage.WarmImageArtifacts, bool, error)
	RecordImageUse(imageReference string, usedAt time.Time) error
}

// serialBroker manages each VM's serial console PTY.
type serialBroker interface {
	Open(id string) error
	Close(id string) error
}

// Runtime manages Firecracker virtual machines on one host. Each VM runs as a
// systemd unit, so the process lifetime belongs to systemd and this package
// controls only what happens inside it.
type Runtime struct {
	configuration         Config
	units                 platform.UnitManager
	virtualMachineStorage virtualMachineStorage
	imageStore            imageStore
	serialBroker          serialBroker
	sshSlots              chan struct{}
	logger                *slog.Logger
}

var _ vm.Runtime = (*Runtime)(nil)

// NewRuntime returns a Firecracker runtime.
func NewRuntime(
	configuration Config,
	units platform.UnitManager,
	virtualMachineStorage virtualMachineStorage,
	imageStore imageStore,
	serialBroker serialBroker,
	logger *slog.Logger,
) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}

	return &Runtime{
		configuration:         configuration,
		units:                 units,
		virtualMachineStorage: virtualMachineStorage,
		imageStore:            imageStore,
		serialBroker:          serialBroker,
		sshSlots:              make(chan struct{}, maxConcurrentSSHSessions),
		logger:                logger,
	}
}

// Inspect returns the current systemd and Firecracker state.
func (runtime *Runtime) Inspect(ctx context.Context, input vm.RuntimeMachine) (vm.RuntimeStatus, error) {
	state, err := runtime.newMachine(input).status(ctx)
	if err != nil {
		return vm.RuntimeStatus{}, fmt.Errorf("inspect Firecracker VM: %w", err)
	}

	return vm.RuntimeStatus{State: state}, nil
}

// Start launches a Firecracker virtual machine. StartNormal keeps the current
// warm-image and cold-boot behavior. StartFromSleepSnapshot restores and resumes
// the VM-local snapshot. StartFromSleepSnapshotPaused restores it but leaves the
// vCPUs paused.
func (runtime *Runtime) Start(ctx context.Context, input vm.RuntimeMachine, mode vm.StartMode) error {
	switch mode {
	case vm.StartNormal:
		return runtime.newMachine(input).Start(ctx)
	case vm.StartFromSleepSnapshot:
		return runtime.newMachine(input).startFromSnapshot(ctx, true)
	case vm.StartFromSleepSnapshotPaused:
		return runtime.newMachine(input).startFromSnapshot(ctx, false)
	default:
		return fmt.Errorf("unknown start mode %d", mode)
	}
}

// Stop stops a Firecracker virtual machine. StopShutdown keeps the current
// Ctrl+Alt+Del and bounded kill. StopWithSleepSnapshot pauses the guest,
// publishes a full snapshot, and terminates the process. A warm stop reports the
// published snapshot generation and its creation time.
func (runtime *Runtime) Stop(ctx context.Context, input vm.RuntimeMachine, mode vm.StopMode) (vm.StopOutcome, error) {
	switch mode {
	case vm.StopShutdown:
		return vm.StopOutcome{}, runtime.newMachine(input).Stop(ctx)
	case vm.StopWithSleepSnapshot:
		return runtime.newMachine(input).warmStop(ctx)
	default:
		return vm.StopOutcome{}, fmt.Errorf("unknown stop mode %d", mode)
	}
}

// InspectSleepSnapshot returns the newest valid sleep snapshot for a VM. It
// reports vm.ErrNotFound when none exists and preserves an invalid-manifest error
// for an operator to inspect.
func (runtime *Runtime) InspectSleepSnapshot(_ context.Context, input vm.RuntimeMachine) (vm.SleepSnapshot, error) {
	machine := runtime.newMachine(input)
	snapshot, err := runtime.configuration.latestValidSnapshot(machine.snapshotRequirement())
	if errors.Is(err, errSnapshotNotFound) {
		return vm.SleepSnapshot{}, vm.ErrNotFound
	}
	if err != nil {
		return vm.SleepSnapshot{}, err
	}
	return vm.SleepSnapshot{Generation: snapshot.Generation, CreatedAt: snapshot.Manifest.CreatedAt}, nil
}

// DiscardSleepSnapshot clears the runtime unit of a warm-stopped VM and removes
// its sleep snapshots, so a later start cold boots with the current shape.
func (runtime *Runtime) DiscardSleepSnapshot(ctx context.Context, input vm.RuntimeMachine) error {
	if err := runtime.newMachine(input).cleanupSystemd(ctx); err != nil {
		return fmt.Errorf("discard sleep snapshot: %w", err)
	}
	return runtime.purgeSnapshots(input.ID)
}

// Pause pauses a running Firecracker virtual machine.
func (runtime *Runtime) Pause(ctx context.Context, input vm.RuntimeMachine) error {
	return runtime.newMachine(input).Pause(ctx)
}

// Resume resumes a paused Firecracker virtual machine.
func (runtime *Runtime) Resume(ctx context.Context, input vm.RuntimeMachine) error {
	return runtime.newMachine(input).Resume(ctx)
}

// Remove stops the process and removes every runtime-owned file, so the VM ID
// can be used again.
func (runtime *Runtime) Remove(ctx context.Context, input vm.RuntimeMachine) error {
	if err := runtime.newMachine(input).cleanupSystemd(ctx); err != nil {
		return err
	}

	if err := os.Remove(runtime.configuration.socketPath(input.ID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove Firecracker socket: %w", err)
	}
	if err := os.Remove(runtime.configuration.jailerEnvironmentPath(input.ID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove jailer environment: %w", err)
	}
	if err := os.RemoveAll(filepath.Dir(runtime.configuration.chrootRoot(input.ID))); err != nil {
		return fmt.Errorf("remove Firecracker jail: %w", err)
	}
	if err := runtime.purgeSnapshots(input.ID); err != nil {
		return err
	}

	return nil
}

// RefreshDisk applies current disk limits and refreshes the guest block device.
// A VM that is not live has nothing to refresh: its next boot reads the limits.
func (runtime *Runtime) RefreshDisk(ctx context.Context, input vm.RuntimeMachine) error {
	status, err := runtime.Inspect(ctx, input)
	if err != nil {
		return err
	}
	if status.State != vm.StateRunning && status.State != vm.StatePaused {
		return nil
	}

	return api.New(runtime.configuration.socketPath(input.ID)).PatchDrive(ctx, api.PartialDrive{
		DriveID:     rootDriveIdentifier,
		PathOnHost:  rootDrivePath,
		RateLimiter: driveRateLimiter(input.Specification.Disk),
	})
}
