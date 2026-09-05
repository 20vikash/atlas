package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// Inspect returns the current systemd and Firecracker state.
func (runtime *Runtime) Inspect(ctx context.Context, input vm.RuntimeMachine) (vm.RuntimeStatus, error) {
	machine := runtime.machine(input)
	state, err := machine.status(ctx)
	if err != nil {
		return vm.RuntimeStatus{}, fmt.Errorf("inspect Firecracker VM: %w", err)
	}
	return vm.RuntimeStatus{State: state}, nil
}

// Start launches a stopped Firecracker virtual machine.
func (runtime *Runtime) Start(ctx context.Context, input vm.RuntimeMachine) error {
	return runtime.machine(input).Start(ctx)
}

// Stop stops a Firecracker virtual machine.
func (runtime *Runtime) Stop(ctx context.Context, input vm.RuntimeMachine) error {
	return runtime.machine(input).Stop(ctx)
}

// Pause pauses a running Firecracker virtual machine.
func (runtime *Runtime) Pause(ctx context.Context, input vm.RuntimeMachine) error {
	return runtime.machine(input).Pause(ctx)
}

// Resume resumes a paused Firecracker virtual machine.
func (runtime *Runtime) Resume(ctx context.Context, input vm.RuntimeMachine) error {
	return runtime.machine(input).Resume(ctx)
}

// Remove stops the process and removes runtime-owned files.
func (runtime *Runtime) Remove(ctx context.Context, input vm.RuntimeMachine) error {
	machine := runtime.machine(input)
	if err := machine.cleanupSystemd(ctx); err != nil {
		return err
	}
	if err := os.Remove(runtime.configuration.sockPath(input.ID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove Firecracker socket: %w", err)
	}
	if err := os.Remove(runtime.configuration.jailerEnvironmentPath(input.ID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove jailer environment: %w", err)
	}
	if err := os.RemoveAll(filepath.Dir(runtime.configuration.chrootRoot(input.ID))); err != nil {
		return fmt.Errorf("remove Firecracker jail: %w", err)
	}
	return nil
}

// RefreshMetadata replaces the current MMDS data for an active process.
func (runtime *Runtime) RefreshMetadata(ctx context.Context, input vm.RuntimeMachine) error {
	status, err := runtime.Inspect(ctx, input)
	if err != nil {
		return err
	}
	if status.State != vm.StateRunning && status.State != vm.StatePaused {
		return nil
	}
	return api.New(runtime.configuration.sockPath(input.ID)).PutMMDS(ctx, metadataServiceData(
		input.ID,
		input.NetworkInterface.GuestIPAddress,
		input.NetworkInterface.MACAddress,
		input.Specification,
	))
}

// RefreshDisk applies current disk limits and refreshes the guest block device.
func (runtime *Runtime) RefreshDisk(ctx context.Context, input vm.RuntimeMachine) error {
	status, err := runtime.Inspect(ctx, input)
	if err != nil {
		return err
	}
	if status.State != vm.StateRunning && status.State != vm.StatePaused {
		return nil
	}
	return api.New(runtime.configuration.sockPath(input.ID)).PatchDrive(ctx, api.PartialDrive{
		DriveID:     rootDriveIdentifier,
		PathOnHost:  rootDrivePath,
		RateLimiter: driveRateLimiter(input.Specification.Disk),
	})
}

func (runtime *Runtime) machine(input vm.RuntimeMachine) *machine {
	return runtime.newMachine(input)
}

var _ vm.Runtime = (*Runtime)(nil)
