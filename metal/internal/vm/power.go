package vm

import "context"

// SetPowerState stores a power state.
func (manager *Manager) SetPowerState(ctx context.Context, identifier string, state State) error {
	if state != StateRunning && state != StateStopped && state != StatePaused {
		return ErrConflict
	}
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == state {
			return false, nil
		}
		record.State = state
		return true, nil
	})
}
