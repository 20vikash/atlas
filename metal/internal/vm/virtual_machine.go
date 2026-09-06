package vm

import "context"

// virtualMachine contains the manager and identity for one operation.
type virtualMachine struct {
	manager    *Manager
	identifier string
}

func (manager *Manager) newVirtualMachine(identifier string) virtualMachine {
	return virtualMachine{manager: manager, identifier: identifier}
}

func (virtualMachine virtualMachine) lock(ctx context.Context) (func(), error) {
	return virtualMachine.manager.operationLocks.lock(ctx, virtualMachine.identifier)
}

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

func (virtualMachine virtualMachine) networkRequest(record DesiredRecord) NetworkRequest {
	return NetworkRequest{
		VirtualMachineID: record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Configuration:    record.Specification.Network,
	}
}

func (virtualMachine virtualMachine) runtimeConfiguration(record DesiredRecord, networkInterface NetworkInterface) RuntimeMachine {
	return RuntimeMachine{
		ID:               record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Specification:    cloneSpecification(record.Specification),
		NetworkInterface: networkInterface,
	}
}
