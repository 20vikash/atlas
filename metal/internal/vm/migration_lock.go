package vm

import "os"

// Migration records own source locks and target reservations. Source records
// block mutation and reconciliation; target records reserve incoming VM IDs.

// isSourceLocked reports whether a migration holds the VM as source.
func (manager *Manager) isSourceLocked(virtualMachineID string) bool {
	return fileExists(sourceRecordPath(manager.configuration.MachinesDirectory, virtualMachineID))
}

// isTargetReserved reports whether a migration reserves this VM ID. Terminal
// records release it; unreadable records remain reserved.
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

// isMigrating reports whether any migration role holds the VM.
func (manager *Manager) isMigrating(virtualMachineID string) bool {
	return manager.isSourceLocked(virtualMachineID) || manager.isTargetReserved(virtualMachineID)
}

// assertNotSourceLocked rejects mutation while the VM is a source.
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
