package firecracker

import (
	"context"

	"github.com/frappe/atlas/metal/internal/vm"
)

func (m *machine) status(ctx context.Context) (vm.State, error) {
	unitStatus, err := m.d.units.Status(ctx, m.input.ID)
	if err != nil {
		return "", err
	}
	return m.state(ctx, unitStatus)
}

// Pause halts the guest virtual CPUs.
func (m *machine) Pause(ctx context.Context) error {
	return m.pauseUnlocked(ctx)
}

func (m *machine) pauseUnlocked(ctx context.Context) error {
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
	return m.resumeUnlocked(ctx)
}

func (m *machine) resumeUnlocked(ctx context.Context) error {
	state, err := m.status(ctx)
	if err != nil {
		return err
	}
	if state != vm.StatePaused {
		return vm.ErrConflict
	}
	return m.api.Resume(ctx)
}
