package vm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// sourceLockFileName marks a VM that a migration holds as its source. The marker
// lives in the VM directory next to its records, so a mutation or a reconcile
// pass tests it with one stat and no knowledge of migration state.
//
// MigrationManager is the only writer. It is the authoritative migration state in
// migrations/<id>/source.json that this marker guards, so the marker is written
// before that record and cleared after it. A partial write then leaves the VM
// blocked with no record, which recovery clears, never a record with no block.
// While the marker exists, the manager blocks every mutation and the reconciler
// skips the VM. Reads stay available.
const sourceLockFileName = "migration.lock"

// setSourceLock marks a VM as a migration source.
func (manager *Manager) setSourceLock(identifier string) error {
	directory := filepath.Join(manager.configuration.MachinesDirectory, identifier)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create machine directory: %w", err)
	}
	if err := platform.WriteFile(manager.sourceLockPath(identifier), nil, 0o640); err != nil {
		return fmt.Errorf("write source lock: %w", err)
	}
	return nil
}

// clearSourceLock removes the migration source marker.
func (manager *Manager) clearSourceLock(identifier string) error {
	if err := os.Remove(manager.sourceLockPath(identifier)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear source lock: %w", err)
	}
	return nil
}

// isSourceLocked reports whether a migration holds this VM as its source.
func (manager *Manager) isSourceLocked(identifier string) bool {
	_, err := os.Stat(manager.sourceLockPath(identifier))
	return err == nil
}

// assertNotSourceLocked rejects a mutation while the VM is a migration source.
func (manager *Manager) assertNotSourceLocked(identifier string) error {
	if manager.isSourceLocked(identifier) {
		return ErrConflict
	}
	return nil
}

func (manager *Manager) sourceLockPath(identifier string) string {
	return filepath.Join(manager.configuration.MachinesDirectory, identifier, sourceLockFileName)
}
