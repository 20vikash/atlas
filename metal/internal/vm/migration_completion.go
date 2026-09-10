package vm

import (
	"context"
	"errors"
)

// RequestFinish records the finish intent for a ready migration and is valid
// only after the target reports ready. A repeat is safe. A pending abort wins,
// so a finish after an abort request conflicts.
func (m *MigrationManager) RequestFinish(ctx context.Context, migrationID string) error {
	virtualMachineID, _, err := m.store.findTarget(migrationID)
	if err != nil {
		return err
	}
	unlock, err := m.locks.lock(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.readTarget(virtualMachineID)
	if err != nil {
		return err
	}
	if record.Status == MigrationCompleted {
		return nil
	}
	if record.AbortRequested || record.Status != MigrationReady {
		return ErrConflict
	}
	if record.FinishRequested {
		return nil
	}
	record.FinishRequested = true
	return m.store.writeTarget(record)
}

// advanceFinish commits a ready migration: it destroys the source, then writes
// a compact completed record and removes the token. Each step is idempotent, so
// a restart repeats safely.
func (m *MigrationManager) advanceFinish(ctx context.Context, record TargetMigrationRecord) error {
	if record.Phase != PhaseFinishing {
		record.Phase = PhaseFinishing
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	token, err := m.store.readToken(record.VirtualMachineID)
	if err == nil {
		if err := m.source.FinishSource(ctx, record.Source, record.ID, token); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	if err := m.store.writeTarget(terminalTargetRecord(record, MigrationCompleted, m.now())); err != nil {
		return err
	}
	return m.store.removeToken(record.VirtualMachineID)
}
