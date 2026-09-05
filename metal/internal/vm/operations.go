package vm

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"
)

// SetDesiredState stores a requested lifecycle state.
func (manager *Manager) SetDesiredState(ctx context.Context, identifier string, state State) error {
	if !IsDesiredState(state) {
		return ErrConflict
	}
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == state {
			return false, nil
		}
		record.State = state
		return true, nil
	})
}

// RequestRestart stores durable restart intent.
func (manager *Manager) RequestRestart(ctx context.Context, identifier string) error {
	unlock, err := manager.operationLocks.lock(ctx, identifier)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := manager.store.readDesired(identifier)
	if err != nil {
		return err
	}
	if record.State != StateRunning {
		return ErrConflict
	}
	record.RestartGeneration++
	return manager.store.writeDesired(record)
}

// ResizeCompute stores a requested CPU and memory shape.
func (manager *Manager) ResizeCompute(ctx context.Context, identifier string, virtualCPUCount, memoryMiB int) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		observed, err := manager.store.readObserved(identifier)
		if err != nil {
			return false, err
		}
		if observed.State != StateStopped {
			return false, ErrConflict
		}
		if record.Specification.VCPUs == virtualCPUCount &&
			record.Specification.MemoryMiB == memoryMiB &&
			record.State == StateRunning {
			return false, nil
		}
		record.Specification.VCPUs = virtualCPUCount
		record.Specification.MemoryMiB = memoryMiB
		record.State = StateRunning
		return true, nil
	})
}

// ResizeDisk stores a larger requested disk size.
func (manager *Manager) ResizeDisk(ctx context.Context, identifier string, diskMiB int) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		switch {
		case diskMiB < record.Specification.DiskMiB:
			return false, ErrConflict
		case diskMiB == record.Specification.DiskMiB:
			return false, nil
		default:
			record.Specification.DiskMiB = diskMiB
			return true, nil
		}
	})
}

// UpdateDiskLimits stores complete disk rate limits.
func (manager *Manager) UpdateDiskLimits(ctx context.Context, identifier string, limits Disk) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.Specification.Disk == limits {
			return false, nil
		}
		record.Specification.Disk = limits
		return true, nil
	})
}

// UpdateNetwork stores mutable network settings.
func (manager *Manager) UpdateNetwork(ctx context.Context, identifier string, update NetworkUpdate) error {
	unlock, err := manager.operationLocks.lock(ctx, identifier)
	if err != nil {
		return err
	}
	defer unlock()
	manager.allocationMutex.Lock()
	defer manager.allocationMutex.Unlock()
	record, err := manager.store.readDesired(identifier)
	if err != nil {
		return err
	}
	desired := record.Specification.Network
	desired.Egress = update.Egress
	desired.PublicIPv4 = update.PublicIPv4
	desired.PrivateNetworkThroughputMiBps = update.PrivateNetworkThroughputMiBps
	desired.PublicNetworkThroughputMiBps = update.PublicNetworkThroughputMiBps
	if desired == record.Specification.Network {
		return nil
	}
	if inUse, err := manager.publicIPv4InUse(identifier, desired.PublicIPv4); err != nil {
		return err
	} else if inUse {
		return ErrConflict
	}
	record.Specification.Network = desired
	record.Generation++
	return manager.store.writeDesired(record)
}

// ReplaceSSHKeys stores the complete desired SSH key list.
func (manager *Manager) ReplaceSSHKeys(ctx context.Context, identifier string, sshKeys []string) error {
	changed, err := manager.mutateAndReport(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if slices.Equal(record.Specification.SSHKeys, sshKeys) {
			return false, nil
		}
		record.Specification.SSHKeys = slices.Clone(sshKeys)
		return true, nil
	})
	if err != nil || !changed {
		return err
	}
	return manager.refreshMetadata(ctx, identifier)
}

// ReplaceMetadata stores the complete desired guest metadata map.
func (manager *Manager) ReplaceMetadata(ctx context.Context, identifier string, metadata map[string]string) error {
	changed, err := manager.mutateAndReport(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if maps.Equal(record.Specification.Metadata, metadata) {
			return false, nil
		}
		record.Specification.Metadata = maps.Clone(metadata)
		return true, nil
	})
	if err != nil || !changed {
		return err
	}
	return manager.refreshMetadata(ctx, identifier)
}

// Delete stores desired destruction.
func (manager *Manager) Delete(ctx context.Context, identifier string) error {
	return manager.SetDesiredState(ctx, identifier, StateDestroyed)
}

// ConnectSSH opens one guest SSH session.
func (manager *Manager) ConnectSSH(ctx context.Context, identifier string) (SSHConn, error) {
	desired, err := manager.store.readDesired(identifier)
	if err != nil {
		return nil, err
	}
	interfaceState, err := manager.network.Ensure(ctx, networkRequest(desired))
	if err != nil {
		return nil, err
	}
	return manager.runtime.ConnectSSH(ctx, runtimeMachine(desired, interfaceState))
}

// CreateSnapshot stages one machine image snapshot.
func (manager *Manager) CreateSnapshot(ctx context.Context, identifier string) (StagedSnapshot, error) {
	unlock, err := manager.operationLocks.lock(ctx, identifier)
	if err != nil {
		return StagedSnapshot{}, err
	}
	defer unlock()
	desired, err := manager.store.readDesired(identifier)
	if err != nil {
		return StagedSnapshot{}, err
	}
	observed, err := manager.store.readObserved(identifier)
	if err != nil {
		return StagedSnapshot{}, err
	}
	operationID := newOperationID()
	machine := runtimeMachine(desired, observed.NetworkInterface)
	status, err := manager.inspect(ctx, identifier, machine, &observed, operationID)
	if err != nil {
		return StagedSnapshot{}, err
	}
	if status.State != StateRunning && status.State != StatePaused && status.State != StateStopped {
		observed.State = status.State
		observed.Phase = ""
		observed.OperationID = ""
		observed.OperationStartedAt = time.Time{}
		observed.Error = nil
		observed.UpdatedAt = time.Now().UTC()
		if err := manager.store.writeObserved(identifier, observed); err != nil {
			return StagedSnapshot{}, err
		}
		return StagedSnapshot{}, ErrConflict
	}
	var snapshot StagedSnapshot
	err = manager.runOperation(ctx, identifier, &observed, operationID, phaseSnapshot, func() (operationError error) {
		resume := status.State == StateRunning
		if resume {
			if err := manager.runtime.Pause(ctx, machine); err != nil {
				return err
			}
			defer func() {
				operationError = errors.Join(operationError, manager.runtime.Resume(context.WithoutCancel(ctx), machine))
			}()
		}
		snapshot, operationError = manager.snapshots.Stage(ctx, SnapshotRequest{
			VirtualMachineID: identifier,
			ImageReference:   desired.Specification.Image.Name,
		})
		return operationError
	})
	if err != nil {
		return StagedSnapshot{}, err
	}
	observed.State = status.State
	observed.Phase = ""
	observed.OperationID = ""
	observed.OperationStartedAt = time.Time{}
	observed.Error = nil
	observed.UpdatedAt = time.Now().UTC()
	if err := manager.store.writeObserved(identifier, observed); err != nil {
		return StagedSnapshot{}, err
	}
	return snapshot, nil
}

func (manager *Manager) refreshMetadata(ctx context.Context, identifier string) error {
	desired, err := manager.store.readDesired(identifier)
	if err != nil {
		return err
	}
	interfaceState, err := manager.network.Ensure(ctx, networkRequest(desired))
	if err != nil {
		return err
	}
	err = manager.runtime.RefreshMetadata(ctx, runtimeMachine(desired, interfaceState))
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

func (manager *Manager) mutate(ctx context.Context, identifier string, change func(*DesiredRecord) (bool, error)) error {
	_, err := manager.mutateAndReport(ctx, identifier, change)
	return err
}

func (manager *Manager) mutateAndReport(ctx context.Context, identifier string, change func(*DesiredRecord) (bool, error)) (bool, error) {
	unlock, err := manager.operationLocks.lock(ctx, identifier)
	if err != nil {
		return false, err
	}
	defer unlock()
	record, err := manager.store.readDesired(identifier)
	if err != nil {
		return false, err
	}
	changed, err := change(&record)
	if err != nil {
		return false, err
	}
	if changed {
		record.Generation++
	}
	if !changed {
		return false, nil
	}
	if err := manager.store.writeDesired(record); err != nil {
		return false, err
	}
	return changed, nil
}

func networkRequest(record DesiredRecord) NetworkRequest {
	return NetworkRequest{
		VirtualMachineID: record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Configuration:    record.Specification.Network,
	}
}

func runtimeMachine(record DesiredRecord, interfaceState NetworkInterface) RuntimeMachine {
	return RuntimeMachine{
		ID:               record.ID,
		UserID:           record.UserID,
		GroupID:          record.GroupID,
		Specification:    cloneSpecification(record.Specification),
		NetworkInterface: interfaceState,
	}
}
