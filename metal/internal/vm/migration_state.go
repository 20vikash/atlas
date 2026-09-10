package vm

import (
	"context"
	"fmt"
)

// The VM manager owns the runtime and network work of a migration. The
// migration manager calls these methods while it holds the VM operation lock,
// so they never take the lock again.

// NormalizeSourceToStopped brings a migration source VM to a clean stopped
// runtime state. Saved guest memory is not transferred, so a saved state is
// restored and then stopped. The caller holds the VM operation lock.
func (manager *Manager) NormalizeSourceToStopped(ctx context.Context, virtualMachineID string) error {
	desired, observed, err := manager.newVirtualMachine(virtualMachineID).records()
	if err != nil {
		return err
	}
	machine := runtimeMachine(desired, observed.NetworkInterface)
	status, err := manager.runtime.Inspect(ctx, machine)
	if err != nil {
		return fmt.Errorf("inspect migration source %s: %w", virtualMachineID, err)
	}

	switch status.State {
	case StateRunning, StateCreated:
		return manager.runtime.Stop(ctx, machine)
	case StatePaused:
		if err := manager.runtime.Resume(ctx, machine); err != nil {
			return err
		}
		return manager.runtime.Stop(ctx, machine)
	case StateStopped:
		if !status.HasSavedState {
			return nil
		}
		if err := manager.runtime.Restore(ctx, machine); err != nil {
			return err
		}
		return manager.runtime.Stop(ctx, machine)
	default:
		return &TransitionError{DesiredState: StateStopped, ObservedState: status.State}
	}
}

// LimitSourceDisk applies a temporary combined read and write bandwidth limit to
// the live source drive before a stream. It never raises the configured limit
// and returns the value it applied. A value of zero or less does nothing.
func (manager *Manager) LimitSourceDisk(ctx context.Context, virtualMachineID string, throughputMiBps int) (int, error) {
	if throughputMiBps <= 0 {
		return 0, nil
	}
	desired, observed, err := manager.newVirtualMachine(virtualMachineID).records()
	if err != nil {
		return 0, err
	}
	if configured := desired.Specification.Disk.ThroughputMiBps; configured > 0 && configured < throughputMiBps {
		throughputMiBps = configured
	}
	machine := runtimeMachine(desired, observed.NetworkInterface)
	if err := manager.runtime.LimitDiskThroughput(ctx, machine, throughputMiBps); err != nil {
		return 0, err
	}
	return throughputMiBps, nil
}

// RemoveMigrationNetwork releases the host network of a migration source VM. The
// target creates its own network after the source network is gone. The caller
// holds the VM operation lock.
func (manager *Manager) RemoveMigrationNetwork(ctx context.Context, virtualMachineID string) error {
	desired, err := manager.store.readDesired(virtualMachineID)
	if err != nil {
		return err
	}
	return manager.network.Release(ctx, NetworkReleaseRequest{
		VirtualMachineID:  desired.ID,
		UserID:            desired.UserID,
		WireGuardMeshIPv6: desired.Specification.Network.WireGuardMeshIPv6,
	})
}
