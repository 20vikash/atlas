package vm

// A source record blocks mutation and reconciliation; a destination record reserves
// an incoming VM ID. The migration package owns those records and answers
// through the injected MigrationGuard. Without a guard, no VM is migrating.

// isSourceLocked reports whether a migration holds the VM as a source.
func (manager *Manager) isSourceLocked(virtualMachineID string) bool {
	return manager.migrationGuard != nil && manager.migrationGuard.IsSourceLocked(virtualMachineID)
}

// isDestinationReserved reports whether a migration reserves this VM ID.
func (manager *Manager) isDestinationReserved(virtualMachineID string) bool {
	return manager.migrationGuard != nil && manager.migrationGuard.IsDestinationReserved(virtualMachineID)
}

// isMigrating reports whether any migration role holds the VM.
func (manager *Manager) isMigrating(virtualMachineID string) bool {
	return manager.isSourceLocked(virtualMachineID) || manager.isDestinationReserved(virtualMachineID)
}

// assertNotSourceLocked rejects mutation while the VM is a source.
func (manager *Manager) assertNotSourceLocked(virtualMachineID string) error {
	if manager.isSourceLocked(virtualMachineID) {
		return ErrConflict
	}
	return nil
}
