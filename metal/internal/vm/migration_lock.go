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
// incoming target VM.
func (manager *Manager) isTargetReserved(virtualMachineID string) bool {
	return fileExists(targetRecordPath(manager.configuration.MachinesDirectory, virtualMachineID))
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
