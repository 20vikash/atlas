package vm

import "context"

// SetPowerState stores a power state and clears warm-stop intent.
func (manager *Manager) SetPowerState(ctx context.Context, identifier string, state State) error {
	if state != StateRunning && state != StateStopped && state != StatePaused {
		return ErrConflict
	}
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == state && !record.WarmStop {
			return false, nil
		}
		record.State = state
		record.WarmStop = false
		return true, nil
	})
}

// StopWarm stores a stop that saves a memory snapshot.
func (manager *Manager) StopWarm(ctx context.Context, identifier string) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == StateDestroyed {
			return false, ErrConflict
		}
		if record.State == StateStopped && record.WarmStop {
			return false, nil
		}
		record.State = StateStopped
		record.WarmStop = true
		return true, nil
	})
}
