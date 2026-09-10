package vm

import "os"

// The source lock and the target reservation are derived from the migration
// records under a VM directory, so there is one owner and no separate marker.
// A source record in machines/<vm-id>/migration/ blocks every VM mutation and
// pauses reconciliation. A target record reserves the VM ID for an incoming VM.
// Reads stay available.

// isSourceLocked reports whether a migration holds this VM as its source.
func (manager *Manager) isSourceLocked(virtualMachineID string) bool {
	return fileExists(sourceRecordPath(manager.configuration.MachinesDirectory, virtualMachineID))
}

// isTargetReserved reports whether a migration reserves this VM ID for an
// incoming target VM. A terminal record no longer reserves the VM. An unreadable
// record stays reserved, so a corrupt record locks conservatively.
func (manager *Manager) isTargetReserved(virtualMachineID string) bool {
	store := newMigrationStore(manager.configuration.MachinesDirectory)
	if !store.has(store.targetPath(virtualMachineID)) {
		return false
	}
	record, err := store.readTarget(virtualMachineID)
	if err != nil {
		return true
	}
	return !isTerminalStatus(record.Status)
}

// isMigrating reports whether any migration role holds this VM. Normal VM
// listing, get, and reconciliation skip a VM in this state.
func (manager *Manager) isMigrating(virtualMachineID string) bool {
	return manager.isSourceLocked(virtualMachineID) || manager.isTargetReserved(virtualMachineID)
}

// assertNotSourceLocked rejects a mutation while the VM is a migration source.
func (manager *Manager) assertNotSourceLocked(virtualMachineID string) error {
	if manager.isSourceLocked(virtualMachineID) {
		return ErrConflict
	}
	return nil
}

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
