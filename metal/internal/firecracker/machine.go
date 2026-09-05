package firecracker

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

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
