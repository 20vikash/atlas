package vm

import (
	"context"
	"errors"
	"fmt"
)

// phaseWakeRestore names the host operation that restores a sleeping VM after a
// network wake event.
const phaseWakeRestore = "wake-restore"

// WakeFromNetwork restores a sleeping VM after a host-to-guest packet armed its
// wake. It takes the VM lock, validates the event against the current records,
// restores the snapshot, and reports running. A stale or duplicate event is a
// safe no-op, so a normal reconcile pass still owns every real state change.
func (manager *Manager) WakeFromNetwork(ctx context.Context, event NetworkWakeEvent) error {
	virtualMachine := manager.newVirtualMachine(event.VirtualMachineID)
	unlock, err := virtualMachine.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	desired, observed, err := virtualMachine.records()
	if errors.Is(err, ErrNotFound) {
		// The VM was removed. A late event has nothing to wake.
		return nil
	}
	if err != nil {
		return err
	}

	if !networkWakeIsValid(event, desired, observed) {
		return nil
	}

	operationID := newOperationID()

	var networkInterface NetworkInterface
	if err := manager.runOperation(ctx, desired.ID, &observed, operationID, phaseNetwork, func() error {
		var ensureError error
		networkInterface, ensureError = manager.network.Ensure(ctx, networkRequest(desired))
		return ensureError
	}); err != nil {
		return err
	}
	observed.NetworkInterface = networkInterface
	machine := runtimeMachine(desired, networkInterface)

	return manager.restoreFromNetworkWake(ctx, desired, machine, &observed, operationID)
}

// networkWakeIsValid reports whether a wake event still matches the records. It
// requires the event user ID, a desired running and sleepy VM, an observed
// sleeping state, and a recorded snapshot. A mismatch means a stale or duplicate
// event, which is a safe no-op.
func networkWakeIsValid(event NetworkWakeEvent, desired DesiredRecord, observed ObservedRecord) bool {
	return event.UserID == desired.UserID &&
		desired.State == StateRunning &&
		desired.Specification.IsSleepy &&
		observed.State == StateSleeping &&
		observed.Sleep != nil &&
		observed.Sleep.MemorySnapshotGeneration != 0
}

// restoreFromNetworkWake restores the snapshot and publishes the running state.
// It keeps the VM sleeping and the snapshot on a load failure, so a bad snapshot
// never cold boots. It persists the completed operation before it disarms the
// wake, so a lost disarm cannot lose the running state.
func (manager *Manager) restoreFromNetworkWake(
	ctx context.Context,
	desired DesiredRecord,
	machine RuntimeMachine,
	observed *ObservedRecord,
	operationID string,
) error {
	if err := manager.runOperation(ctx, desired.ID, observed, operationID, phaseWakeRestore, func() error {
		return manager.runtime.Start(ctx, machine, StartFromSleepSnapshot)
	}); err != nil {
		return err
	}

	status, err := manager.inspect(ctx, desired.ID, machine, observed, operationID)
	if err != nil {
		return err
	}
	if status.State != StateRunning {
		return fmt.Errorf("VM %s did not reach running after a network wake restore", desired.ID)
	}

	observed.Sleep = nil
	observed.State = StateRunning
	observed.Generation = desired.Generation
	observed.RestartGeneration = desired.RestartGeneration
	observed.completeOperation()
	if err := manager.store.writeObserved(desired.ID, *observed); err != nil {
		return err
	}

	manager.disarmNetworkWake(desired)
	return nil
}

// armNetworkWake makes the next host-to-guest packet for the VM produce a wake
// event. It returns an error, because a VM that cannot arm must not sleep: it
// would never wake on a packet. It is a no-op when no wake monitor is set.
func (manager *Manager) armNetworkWake(desired DesiredRecord) error {
	if manager.networkWakeMonitor == nil {
		return nil
	}
	request := NetworkActivityRequest{VirtualMachineID: desired.ID, UserID: desired.UserID}
	if err := manager.networkWakeMonitor.ArmNetworkWake(request); err != nil {
		return fmt.Errorf("arm network wake for VM %s: %w", desired.ID, err)
	}
	return nil
}

// disarmNetworkWake stops further wake events for a woken VM. A disarm failure is
// logged, not returned, because the VM is already running and the eBPF program
// only produces one event until the next arm.
func (manager *Manager) disarmNetworkWake(desired DesiredRecord) {
	if manager.networkWakeMonitor == nil {
		return
	}
	request := NetworkActivityRequest{VirtualMachineID: desired.ID, UserID: desired.UserID}
	if err := manager.networkWakeMonitor.DisarmNetworkWake(request); err != nil {
		manager.logger.Warn("disarm network wake failed",
			"component", "vm", "vm_id", desired.ID, "error", err)
	}
}
