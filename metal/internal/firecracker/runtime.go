package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	platform "github.com/frappe/atlas/metal/internal/platform"
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

func (machine *machine) status(ctx context.Context) (vm.State, error) {
	unitStatus, err := machine.d.units.Status(ctx, machine.input.ID)
	if err != nil {
		return "", err
	}
	return machine.state(ctx, unitStatus)
}

// Pause halts the guest virtual CPUs.
func (machine *machine) Pause(ctx context.Context) error {
	state, err := machine.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StateRunning {
		return vm.ErrConflict
	}
	return machine.api.Pause(ctx)
}

// Resume returns a paused guest to the running state.
func (machine *machine) Resume(ctx context.Context) error {
	state, err := machine.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StatePaused {
		return vm.ErrConflict
	}
	return machine.api.Resume(ctx)
}

func (machine *machine) state(ctx context.Context, status platform.Status) (vm.State, error) {
	switch status.ActiveState {
	case "failed":
		return vm.StateFailed, nil
	case "inactive", "deactivating":
		return vm.StateStopped, nil
	case "active":
	default:
		return vm.StateUnknown, nil
	}

	instance, err := machine.api.InstanceInfo(ctx)
	if err != nil {
		return vm.StateUnknown, fmt.Errorf("inspect Firecracker instance: %w", err)
	}
	switch instance.State {
	case "Not started":
		return vm.StateCreated, nil
	case "Running":
		return vm.StateRunning, nil
	case "Paused":
		return vm.StatePaused, nil
	default:
		return vm.StateUnknown, nil
	}
}

var _ vm.Runtime = (*Runtime)(nil)

const defaultStopTimeout = 30 * time.Second

type machine struct {
	d           *Runtime
	input       vm.RuntimeMachine
	api         *api.Client
	stopTimeout time.Duration
}

func (d *Runtime) newMachine(vc vm.RuntimeMachine) *machine {
	return &machine{
		d:           d,
		input:       vc,
		api:         api.New(d.configuration.sockPath(vc.ID)),
		stopTimeout: defaultStopTimeout,
	}
}

func (m *machine) ID() string { return m.input.ID }

// Start boots a virtual machine.
func (m *machine) Start(ctx context.Context) error {
	return m.startUnlocked(ctx)
}

func (m *machine) startUnlocked(ctx context.Context) error {
	if m.d.hasMatchingMemorySnapshot(m.input.Specification) {
		err := m.d.launchWarmImage(ctx, m.input, m.input.Specification.Image.Name)
		if err == nil {
			m.recordImageUse()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.d.logger.Warn("warm boot failed, using cold boot", "virtual_machine_id", m.input.ID, "error", err)
		if err := m.d.virtualMachineStorage.Release(ctx, m.input.ID); err != nil {
			return err
		}
	}

	if err := m.d.relaunch(ctx, m.input); err != nil {
		return err
	}
	if err := m.api.InstanceStart(ctx); err != nil {
		return err
	}
	m.recordImageUse()
	return nil
}

func (m *machine) recordImageUse() {
	if err := m.d.imageStore.RecordImageUse(m.input.Specification.Image.Name, time.Now()); err != nil {
		m.d.logger.Error("record image use failed", "virtual_machine_id", m.input.ID, "error", err)
	}
}

// Stop shuts down the guest or kills it after the timeout.
func (m *machine) Stop(ctx context.Context) error {
	return m.stopUnlocked(ctx)
}

func (m *machine) stopUnlocked(ctx context.Context) error {
	if err := m.shutdownGuest(ctx); err != nil {
		return err
	}
	if _, err := m.d.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.d.consoleBroker.Close(m.input.ID)

	// Clear the failed state after an intentional process stop.
	return m.d.units.ResetFailed(ctx, m.input.ID)
}

func (m *machine) killUnlocked(ctx context.Context) error {
	if err := m.d.units.Kill(ctx, m.input.ID, syscall.SIGKILL); err != nil {
		return err
	}
	if _, err := m.d.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.d.consoleBroker.Close(m.input.ID)
	return m.d.units.ResetFailed(ctx, m.input.ID)
}

func (m *machine) shutdownGuest(ctx context.Context) error {
	if m.api.SendCtrlAltDel(ctx) == nil {
		wait, cancel := context.WithTimeout(ctx, m.stopTimeout)
		defer cancel()
		if _, err := m.d.units.Wait(wait, m.input.ID); err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return m.d.units.Kill(ctx, m.input.ID, syscall.SIGKILL)
}

func (m *machine) cleanupSystemd(ctx context.Context) error {
	if err := m.d.units.Stop(ctx, m.input.ID); err != nil {
		return fmt.Errorf("stop VM unit: %w", err)
	}
	_ = m.d.consoleBroker.Close(m.input.ID)
	if err := m.d.units.ResetFailed(ctx, m.input.ID); err != nil {
		return fmt.Errorf("reset VM unit: %w", err)
	}
	return nil
}
