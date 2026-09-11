package vmmigration

// VMMigration answers the vm.MigrationGuard questions from its own records, so
// the vm package can pause mutation and reconciliation without importing this
// package. The daemon injects it with (*vm.Manager).SetMigrationGuard.

// IsSourceLocked reports whether a migration holds the VM as a source.
func (m *VMMigration) IsSourceLocked(virtualMachineID string) bool {
	return m.store.has(m.store.sourcePath(virtualMachineID))
}

// IsTargetReserved reports whether a migration reserves this VM ID. A terminal
// record releases it; an unreadable record stays reserved.
func (m *VMMigration) IsTargetReserved(virtualMachineID string) bool {
	if !m.store.has(m.store.targetPath(virtualMachineID)) {
		return false
	}
	record, err := m.store.readTarget(virtualMachineID)
	if err != nil {
		return true
	}
	return !isTerminalStatus(record.Status)
}
