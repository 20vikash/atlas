package vm

import (
	"context"
	"errors"
	"maps"
	"slices"
)

// SetPowerState stores a requested power state. A plain power request clears any
// warm-stop intent, so the next stop shuts the guest down and the next start
// discards the snapshot.
func (manager *Manager) SetPowerState(ctx context.Context, identifier string, state State) error {
	if state != StateRunning && state != StateStopped && state != StatePaused {
		return ErrConflict
	}
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == state && !record.WarmStop {
			return false, nil
		}
		record.State = state
		record.WarmStop = false
		return true, nil
	})
}

// StopWarm stores a stop that saves a memory snapshot. The VM state becomes
// stopped and a later start resumes from the snapshot. It raises the generation
// only when the intent changes.
func (manager *Manager) StopWarm(ctx context.Context, identifier string) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == StateDestroyed {
			return false, ErrConflict
		}
		if record.State == StateStopped && record.WarmStop {
			return false, nil
		}
		record.State = StateStopped
		record.WarmStop = true
		return true, nil
	})
}

// SetSleepPolicy stores whether the host can sleep an idle VM. It raises the
// generation only when the value changes and keeps the desired power state.
func (manager *Manager) SetSleepPolicy(ctx context.Context, identifier string, isSleepy bool) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == StateDestroyed {
			return false, ErrConflict
		}
		if record.Specification.IsSleepy == isSleepy {
			return false, nil
		}
		record.Specification.IsSleepy = isSleepy
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

// SetCompute stores the complete requested CPU and memory shape.
func (manager *Manager) SetCompute(ctx context.Context, identifier string, virtualCPUCount, memoryMiB int) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		observed, err := manager.store.readObserved(identifier)
		if err != nil {
			return false, err
		}
		if observed.State != StateStopped {
			return false, ErrConflict
		}
		if record.Specification.VirtualCPUCount == virtualCPUCount &&
			record.Specification.MemoryMiB == memoryMiB &&
			record.State == StateRunning {
			return false, nil
		}
		record.Specification.VirtualCPUCount = virtualCPUCount
		record.Specification.MemoryMiB = memoryMiB
		record.State = StateRunning
		return true, nil
	})
}

// SetDisk stores the complete requested disk configuration.
func (manager *Manager) SetDisk(ctx context.Context, identifier string, diskMiB int, limits Disk) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if diskMiB < record.Specification.DiskMiB {
			return false, ErrConflict
		}
		if diskMiB == record.Specification.DiskMiB && limits == record.Specification.Disk {
			return false, nil
		}
		record.Specification.DiskMiB = diskMiB
		record.Specification.Disk = limits
		return true, nil
	})
}

// SetNetwork stores the complete requested network configuration.
func (manager *Manager) SetNetwork(ctx context.Context, identifier string, configuration NetworkConfiguration) error {
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
	if configuration == record.Specification.Network {
		return nil
	}
	if inUse, err := manager.publicIPv4InUse(identifier, configuration.PublicIPv4); err != nil {
		return err
	} else if inUse {
		return ErrConflict
	}
	record.Specification.Network = configuration
	record.Generation++
	return manager.store.writeDesired(record)
}

// ReplaceSSHKeys stores the complete desired SSH key list.
func (manager *Manager) ReplaceSSHKeys(ctx context.Context, identifier string, sshKeys []string) (bool, error) {
	_, err := manager.mutateAndReport(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if slices.Equal(record.Specification.SSHKeys, sshKeys) {
			return false, nil
		}
		record.Specification.SSHKeys = slices.Clone(sshKeys)
		return true, nil
	})
	if err != nil {
		return false, err
	}
	return manager.applyMetadata(ctx, identifier)
}

// ReplaceMetadata stores the complete desired guest metadata map.
func (manager *Manager) ReplaceMetadata(ctx context.Context, identifier string, metadata map[string]string) (bool, error) {
	_, err := manager.mutateAndReport(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if maps.Equal(record.Specification.Metadata, metadata) {
			return false, nil
		}
		record.Specification.Metadata = maps.Clone(metadata)
		return true, nil
	})
	if err != nil {
		return false, err
	}
	return manager.applyMetadata(ctx, identifier)
}

// Delete stores desired destruction.
func (manager *Manager) Delete(ctx context.Context, identifier string) error {
	return manager.mutate(ctx, identifier, func(record *DesiredRecord) (bool, error) {
		if record.State == StateDestroyed {
			return false, nil
		}
		record.State = StateDestroyed
		return true, nil
	})
}

// ConnectSSH opens one guest SSH session.
func (manager *Manager) ConnectSSH(ctx context.Context, identifier string) (SSHConnection, error) {
	virtualMachine := manager.newVirtualMachine(identifier)
	unlock, err := virtualMachine.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	desired, _, err := virtualMachine.records()
	if err != nil {
		return nil, err
	}
	networkInterface, err := manager.network.Ensure(ctx, networkRequest(desired))
	if err != nil {
		return nil, err
	}
	return manager.runtime.ConnectSSH(ctx, runtimeMachine(desired, networkInterface))
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
		observed.completeOperation()
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
	observed.completeOperation()
	if err := manager.store.writeObserved(identifier, observed); err != nil {
		return StagedSnapshot{}, err
	}

	return snapshot, nil
}

// applyMetadata pushes new guest metadata immediately and reports whether the
// guest took it. A failure is not an error: the desired record is already
// stored, so the next reconcile pass applies it. The short timeout keeps an
// unreachable guest from holding up the API response.
func (manager *Manager) applyMetadata(ctx context.Context, identifier string) (bool, error) {
	operationContext, cancel := context.WithTimeout(ctx, manager.configuration.FastApplyTimeout)
	defer cancel()

	if err := manager.refreshMetadata(operationContext, identifier); err != nil {
		return false, nil
	}

	return true, nil
}

// refreshMetadata sends the stored specification to the running guest.
func (manager *Manager) refreshMetadata(ctx context.Context, identifier string) error {
	virtualMachine := manager.newVirtualMachine(identifier)
	unlock, err := virtualMachine.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	desired, observed, err := virtualMachine.records()
	if err != nil {
		return err
	}
	operationID := newOperationID()
	machine := runtimeMachine(desired, observed.NetworkInterface)
	if err := manager.runOperation(ctx, identifier, &observed, operationID, phaseMetadata, func() error {
		return manager.runtime.RefreshMetadata(ctx, machine)
	}); err != nil {
		return err
	}
	observed.completeOperation()

	return manager.store.writeObserved(identifier, observed)
}

// mutate applies one change to the desired record and discards the changed flag.
func (manager *Manager) mutate(ctx context.Context, identifier string, change func(*DesiredRecord) (bool, error)) error {
	_, err := manager.mutateAndReport(ctx, identifier, change)
	return err
}

// mutateAndReport applies one change under the VM lock and reports whether the
// record changed. The generation increases only on a real change, so a repeated
// request does not make the reconciler redo work.
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
	if !changed {
		return false, nil
	}

	record.Generation++
	if err := manager.store.writeDesired(record); err != nil {
		return false, err
	}

	return true, nil
}
