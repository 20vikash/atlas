package firecracker

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// defaultStopTimeout is how long a guest has to shut itself down after
// Ctrl+Alt+Del before it is killed.
const defaultStopTimeout = 30 * time.Second

// machine binds a runtime to one VM for the length of an operation. State comes
// from two places: systemd owns whether the process runs, and Firecracker owns
// what the guest is doing inside it.
type machine struct {
	runtime     *Runtime
	input       vm.RuntimeMachine
	api         *api.Client
	stopTimeout time.Duration
}

// newMachine returns a handle for one VM.
func (runtime *Runtime) newMachine(input vm.RuntimeMachine) *machine {
	return &machine{
		runtime:     runtime,
		input:       input,
		api:         api.New(runtime.configuration.socketPath(input.ID)),
		stopTimeout: defaultStopTimeout,
	}
}

// status reports the VM state from the unit and the guest.
func (m *machine) status(ctx context.Context) (vm.State, error) {
	unitStatus, err := m.runtime.units.Status(ctx, m.input.ID)
	if err != nil {
		return "", err
	}

	return m.state(ctx, unitStatus)
}

// state maps a unit state to a VM state. Only an active unit has a guest worth
// asking, so the other unit states answer on their own.
func (m *machine) state(ctx context.Context, status platform.Status) (vm.State, error) {
	switch status.ActiveState {
	case "failed":
		return vm.StateFailed, nil
	case "inactive", "deactivating":
		return vm.StateStopped, nil
	case "active":
	default:
		return vm.StateUnknown, nil
	}

	instance, err := m.api.InstanceInfo(ctx)
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

// Start boots a VM. It tries the warm image first and falls back to a cold boot,
// because warm boot is an optimization and cold boot is the reliable path. A
// failed warm boot releases the disk it prepared before starting again.
func (m *machine) Start(ctx context.Context) error {
	if m.runtime.hasMatchingMemorySnapshot(m.input.Specification) {
		err := m.runtime.launchWarmImage(ctx, m.input, m.input.Specification.Image.Name)
		if err == nil {
			m.recordImageUse()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		m.runtime.logger.Warn("warm boot failed, using cold boot", "virtual_machine_id", m.input.ID, "error", err)
		if err := m.runtime.virtualMachineStorage.Release(ctx, m.input.ID); err != nil {
			return err
		}
	}

	if err := m.runtime.relaunch(ctx, m.input); err != nil {
		return err
	}
	if err := m.api.InstanceStart(ctx); err != nil {
		return err
	}
	m.recordImageUse()

	return nil
}

// Stop shuts the guest down and clears the unit failure the exit records.
func (m *machine) Stop(ctx context.Context) error {
	if err := m.shutdownGuest(ctx); err != nil {
		return err
	}
	if _, err := m.runtime.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	// An intentional stop leaves the unit failed, because the process was killed.
	return m.runtime.units.ResetFailed(ctx, m.input.ID)
}

// Pause halts the guest virtual CPUs.
func (m *machine) Pause(ctx context.Context) error {
	state, err := m.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StateRunning {
		return vm.ErrConflict
	}

	return m.api.Pause(ctx)
}

// Resume returns a paused guest to the running state.
func (m *machine) Resume(ctx context.Context) error {
	state, err := m.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StatePaused {
		return vm.ErrConflict
	}

	return m.api.Resume(ctx)
}

// kill stops a VM without giving the guest a chance to shut down.
func (m *machine) kill(ctx context.Context) error {
	if err := m.runtime.units.Kill(ctx, m.input.ID, syscall.SIGKILL); err != nil {
		return err
	}
	if _, err := m.runtime.units.Wait(ctx, m.input.ID); err != nil {
		return err
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	return m.runtime.units.ResetFailed(ctx, m.input.ID)
}

// shutdownGuest asks the guest to power off and kills it when it does not. A
// guest with no ACPI handler never answers Ctrl+Alt+Del, so the wait is bounded.
func (m *machine) shutdownGuest(ctx context.Context) error {
	if m.api.SendCtrlAltDel(ctx) == nil {
		wait, cancel := context.WithTimeout(ctx, m.stopTimeout)
		defer cancel()

		if _, err := m.runtime.units.Wait(wait, m.input.ID); err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	return m.runtime.units.Kill(ctx, m.input.ID, syscall.SIGKILL)
}

// cleanupSystemd stops the unit and clears its state, so the ID can be reused.
func (m *machine) cleanupSystemd(ctx context.Context) error {
	if err := m.runtime.units.Stop(ctx, m.input.ID); err != nil {
		return fmt.Errorf("stop VM unit: %w", err)
	}
	_ = m.runtime.serialBroker.Close(m.input.ID)

	if err := m.runtime.units.ResetFailed(ctx, m.input.ID); err != nil {
		return fmt.Errorf("reset VM unit: %w", err)
	}

	return nil
}

// recordImageUse marks the image as used, so pruning keeps it. A failure here
// must not fail a started VM, so it is logged instead.
func (m *machine) recordImageUse() {
	if err := m.runtime.imageStore.RecordImageUse(m.input.Specification.Image.Name, time.Now()); err != nil {
		m.runtime.logger.Error("record image use failed", "virtual_machine_id", m.input.ID, "error", err)
	}
}
