package vm

import "context"

// The migration lifecycle lives in the vm_migration package. That package
// imports this one and drives the manager through the exported methods below.
// This package never imports vm_migration: it only holds the MigrationGuard it
// is given, so the two packages avoid an import cycle.

// MigrationGuard reports whether a migration holds a VM. The vm_migration
// package implements it and injects it with SetMigrationGuard.
type MigrationGuard interface {
	// IsSourceLocked reports whether a migration holds the VM as a source.
	IsSourceLocked(virtualMachineID string) bool
	// IsTargetReserved reports whether a migration reserves this VM ID.
	IsTargetReserved(virtualMachineID string) bool
}

// SetMigrationGuard records the guard that answers migration lock questions.
func (manager *Manager) SetMigrationGuard(guard MigrationGuard) {
	manager.migrationGuard = guard
}

// ReadDesired returns the desired record of one VM.
func (manager *Manager) ReadDesired(virtualMachineID string) (DesiredRecord, error) {
	return manager.store.readDesired(virtualMachineID)
}

// ReadObserved returns the observed record of one VM.
func (manager *Manager) ReadObserved(virtualMachineID string) (ObservedRecord, error) {
	return manager.store.readObserved(virtualMachineID)
}

// WriteDesired replaces the desired record of one VM.
func (manager *Manager) WriteDesired(record DesiredRecord) error {
	return manager.store.writeDesired(record)
}

// WriteObserved replaces the observed record of one VM.
func (manager *Manager) WriteObserved(virtualMachineID string, record ObservedRecord) error {
	return manager.store.writeObserved(virtualMachineID, record)
}

// RemoveRecords removes the VM records of one VM. Migration records stay.
func (manager *Manager) RemoveRecords(virtualMachineID string) error {
	return manager.store.remove(virtualMachineID)
}

// DesiredPath returns the desired record path of one VM.
func (manager *Manager) DesiredPath(virtualMachineID string) string {
	return manager.store.desiredPath(virtualMachineID)
}

// ObservedPath returns the observed record path of one VM.
func (manager *Manager) ObservedPath(virtualMachineID string) string {
	return manager.store.observedPath(virtualMachineID)
}

// AllocateUserID returns a free host user ID. The caller holds the allocation lock.
func (manager *Manager) AllocateUserID() (uint32, error) {
	return manager.allocateUserID()
}

// LockAllocation serializes user ID allocation and returns the unlock.
func (manager *Manager) LockAllocation() func() {
	manager.allocationMutex.Lock()
	return manager.allocationMutex.Unlock
}

// LockOperation takes the per-VM operation lock and returns the unlock.
func (manager *Manager) LockOperation(ctx context.Context, virtualMachineID string) (func(), error) {
	return manager.operationLocks.Lock(ctx, virtualMachineID)
}

// ReleaseStorage releases one VM's disk.
func (manager *Manager) ReleaseStorage(ctx context.Context, virtualMachineID string) error {
	return manager.storage.Release(ctx, virtualMachineID)
}

// MachinesDirectory returns the root directory that holds VM and migration records.
func (manager *Manager) MachinesDirectory() string {
	return manager.configuration.MachinesDirectory
}

// CloneSpecification copies a specification's reference fields for safe reuse.
func CloneSpecification(specification Specification) Specification {
	return cloneSpecification(specification)
}

// ValidIdentifier reports whether an identifier is safe to use as a directory name.
func ValidIdentifier(identifier string) bool {
	return validIdentifier(identifier)
}
