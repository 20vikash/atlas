package vm

import "context"

// machine contains the manager and identity for one operation.
type machine struct {
	manager    *Manager
	identifier string
}

func (manager *Manager) newMachine(identifier string) machine {
	return machine{manager: manager, identifier: identifier}
}

func (machine machine) lock(ctx context.Context) (func(), error) {
	return machine.manager.operationLocks.lock(ctx, machine.identifier)
}

func (machine machine) records() (DesiredRecord, ObservedRecord, error) {
	desired, err := machine.manager.store.readDesired(machine.identifier)
	if err != nil {
		return DesiredRecord{}, ObservedRecord{}, err
	}
	observed, err := machine.manager.store.readObserved(machine.identifier)
	if err != nil {
		return DesiredRecord{}, ObservedRecord{}, err
	}
	return desired, observed, nil
}

func (machine machine) networkRequest(record DesiredRecord) NetworkRequest {
	return NetworkRequest{
		VirtualMachineID: record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Configuration:    record.Specification.Network,
	}
}

func (machine machine) runtimeConfiguration(record DesiredRecord, networkInterface NetworkInterface) RuntimeMachine {
	return RuntimeMachine{
		ID:               record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Specification:    cloneSpecification(record.Specification),
		NetworkInterface: networkInterface,
	}
}
