package vm

import "context"

// virtualMachine binds a manager to one identifier for the length of an
// operation. It holds no VM state of its own.
type virtualMachine struct {
	manager    *Manager
	identifier string
}

// newVirtualMachine returns a handle for one identifier.
func (manager *Manager) newVirtualMachine(identifier string) virtualMachine {
	return virtualMachine{manager: manager, identifier: identifier}
}

// lock serializes operations on this virtual machine.
func (virtualMachine virtualMachine) lock(ctx context.Context) (func(), error) {
	return virtualMachine.manager.operationLocks.lock(ctx, virtualMachine.identifier)
}

// records reads both stored records, so a caller sees one consistent pair.
func (virtualMachine virtualMachine) records() (DesiredRecord, ObservedRecord, error) {
	desired, err := virtualMachine.manager.store.readDesired(virtualMachine.identifier)
	if err != nil {
		return DesiredRecord{}, ObservedRecord{}, err
	}

	observed, err := virtualMachine.manager.store.readObserved(virtualMachine.identifier)
	if err != nil {
		return DesiredRecord{}, ObservedRecord{}, err
	}

	return desired, observed, nil
}

// networkRequest builds the complete desired host network state for a record.
func networkRequest(record DesiredRecord) NetworkRequest {
	return NetworkRequest{
		VirtualMachineID: record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Configuration:    record.Specification.Network,
	}
}

// runtimeMachine builds the complete input for one runtime operation. The
// specification is cloned, so a runtime cannot reach back into stored state.
func runtimeMachine(record DesiredRecord, networkInterface NetworkInterface) RuntimeMachine {
	return RuntimeMachine{
		ID:                      record.ID,
		UserID:                  record.UserID,
		GroupID:                 record.GroupID,
		Specification:           cloneSpecification(record.Specification),
		NetworkInterface:        networkInterface,
		SpecificationGeneration: record.Generation,
		RestartGeneration:       record.RestartGeneration,
	}
}
