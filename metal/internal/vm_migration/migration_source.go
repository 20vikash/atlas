package vmmigration

import (
	"context"
	"errors"
	"fmt"
	"github.com/frappe/atlas/metal/internal/vm"
	"io"
)

// SourceHandshake is the portable state that the source returns to the target.
type SourceHandshake struct {
	Config        PortableConfig
	ObservedState vm.State
}

// SourceSnapshot is the source reply to a snapshot request.
type SourceSnapshot struct {
	Sequence  int
	SizeBytes int64
	GUID      string
}

// migrationSnapshotName includes the migration ID to avoid stale collisions.
func migrationSnapshotName(migrationID string, sequence int) string {
	return fmt.Sprintf("migration-%s-%d", migrationID, sequence)
}

// LockSource locks the source and returns portable config and observed state.
// Missing, failed, unknown, or already-migrating VMs are rejected.
func (m *VMMigration) LockSource(ctx context.Context, migrationID, virtualMachineID string) (SourceHandshake, error) {
	if !vm.ValidIdentifier(migrationID) || !vm.ValidIdentifier(virtualMachineID) {
		return SourceHandshake{}, vm.ErrConflict
	}
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	defer unlock()

	if existing, err := m.store.readSource(virtualMachineID); err == nil {
		if existing.ID != migrationID {
			return SourceHandshake{}, vm.ErrConflict
		}
		return m.sourceHandshake(virtualMachineID)
	} else if !errors.Is(err, vm.ErrNotFound) {
		return SourceHandshake{}, err
	}

	desired, err := m.machines.ReadDesired(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	observed, err := m.machines.ReadObserved(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	if observed.State == vm.StateFailed || observed.State == vm.StateUnknown {
		return SourceHandshake{}, vm.ErrConflict
	}

	record := SourceMigrationRecord{
		ID:               migrationID,
		VirtualMachineID: virtualMachineID,
		OriginalDesired:  desired.State,
		OriginalObserved: observed.State,
		LockedAt:         m.now(),
	}
	if err := m.store.writeSource(record); err != nil {
		return SourceHandshake{}, err
	}
	return m.sourceHandshake(virtualMachineID)
}

// UnlockSource removes snapshots, restores the disk limit, and unlocks the VM.
// It refuses a stopped source before rollback completes.
func (m *VMMigration) UnlockSource(ctx context.Context, migrationID, virtualMachineID string) error {
	if !vm.ValidIdentifier(virtualMachineID) {
		return vm.ErrConflict
	}
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	existing, err := m.store.readSource(virtualMachineID)
	if errors.Is(err, vm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.ID != migrationID {
		return vm.ErrConflict
	}
	if existing.Stopped && !existing.RollbackComplete {
		return vm.ErrConflict
	}
	if err := m.stopSourceStream(ctx, virtualMachineID); err != nil {
		return err
	}
	if err := m.removeMigrationSnapshots(ctx, existing); err != nil {
		return err
	}
	if existing.TemporaryDiskLimitMiBps > 0 && !existing.Stopped {
		if err := m.machines.RefreshSourceDisk(ctx, virtualMachineID); err != nil {
			return err
		}
	}
	return m.store.remove(virtualMachineID)
}

// StartSourceRollback restores a stopped source during abort.
func (m *VMMigration) StartSourceRollback(ctx context.Context, migrationID, virtualMachineID string) error {
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return err
	}
	if record.RollbackComplete {
		return nil
	}
	if err := m.machines.EnsureMigrationNetwork(ctx, virtualMachineID); err != nil {
		return err
	}
	if err := m.machines.RestoreRuntimeState(ctx, virtualMachineID, record.OriginalDesired); err != nil {
		return err
	}
	record.RollbackComplete = true
	return m.store.writeSource(record)
}

// DestroySource removes a stopped source and its migration state. It requires a
// stopped, network-removed source with a final snapshot.
func (m *VMMigration) DestroySource(ctx context.Context, migrationID, virtualMachineID string) error {
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.store.readSource(virtualMachineID)
	if errors.Is(err, vm.ErrNotFound) {
		return m.assertNoSourceRemnant(ctx, virtualMachineID)
	}
	if err != nil {
		return err
	}
	if record.ID != migrationID {
		return vm.ErrConflict
	}
	if !record.Stopped || !record.NetworkRemoved || record.FinalSequence < 1 {
		return vm.ErrConflict
	}
	if err := m.stopSourceStream(ctx, virtualMachineID); err != nil {
		return err
	}

	if !record.DestroyRuntimeComplete {
		if err := m.machines.RemoveMigratedRuntime(ctx, virtualMachineID); err != nil {
			return err
		}
		record.DestroyRuntimeComplete = true
		if err := m.store.writeSource(record); err != nil {
			return err
		}
	}
	if !record.DestroyStorageComplete {
		if err := m.machines.ReleaseStorage(ctx, virtualMachineID); err != nil {
			return err
		}
		record.DestroyStorageComplete = true
		if err := m.store.writeSource(record); err != nil {
			return err
		}
	}
	return m.machines.RemoveRecords(virtualMachineID)
}

// assertNoSourceRemnant rejects leftover VM records or disks.
func (m *VMMigration) assertNoSourceRemnant(ctx context.Context, virtualMachineID string) error {
	if _, err := m.machines.ReadDesired(virtualMachineID); err == nil {
		return vm.ErrConflict
	} else if !errors.Is(err, vm.ErrNotFound) {
		return err
	}
	exists, err := m.transfer.TargetDatasetExists(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	if exists {
		return vm.ErrConflict
	}
	return nil
}

// removeMigrationSnapshots removes every snapshot this migration created.
func (m *VMMigration) removeMigrationSnapshots(ctx context.Context, record SourceMigrationRecord) error {
	highest := max(record.Sequence, record.FinalSequence)
	for sequence := 1; sequence <= highest; sequence++ {
		if err := m.transfer.RemoveSnapshot(ctx, record.VirtualMachineID, migrationSnapshotName(record.ID, sequence)); err != nil {
			return err
		}
	}
	return nil
}

// NextSourceSnapshot records the acknowledged sequence and returns the next
// snapshot. A repeat returns the same unacknowledged snapshot.
func (m *VMMigration) NextSourceSnapshot(ctx context.Context, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error) {
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return SourceSnapshot{}, err
	}

	if receivedSequence > record.AcknowledgedSequence {
		record.AcknowledgedSequence = receivedSequence
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
		if receivedSequence >= 2 {
			_ = m.transfer.RemoveSnapshot(ctx, virtualMachineID, migrationSnapshotName(migrationID, receivedSequence-1))
		}
	}

	if record.Sequence <= record.AcknowledgedSequence {
		next := record.AcknowledgedSequence + 1
		if err := m.transfer.CreateSnapshot(ctx, virtualMachineID, migrationSnapshotName(migrationID, next)); err != nil {
			return SourceSnapshot{}, err
		}
		record.Sequence = next
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	return m.describeSnapshot(ctx, virtualMachineID, migrationID, record.Sequence)
}

// StopSource stops the source, removes its network, and creates the final
// snapshot. Checkpoints make repeats safe.
func (m *VMMigration) StopSource(ctx context.Context, migrationID, virtualMachineID string) (SourceSnapshot, error) {
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return SourceSnapshot{}, err
	}

	if !record.Stopped {
		if err := m.machines.NormalizeSourceToStopped(ctx, virtualMachineID); err != nil {
			return SourceSnapshot{}, err
		}
		record.Stopped = true
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	if !record.NetworkRemoved {
		if err := m.machines.RemoveMigrationNetwork(ctx, virtualMachineID); err != nil {
			return SourceSnapshot{}, err
		}
		record.NetworkRemoved = true
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	if record.FinalSequence == 0 {
		final := record.AcknowledgedSequence + 1
		name := migrationSnapshotName(migrationID, final)
		// Replace any untransferred candidate with the post-stop snapshot.
		if err := m.transfer.RemoveSnapshot(ctx, virtualMachineID, name); err != nil {
			return SourceSnapshot{}, err
		}
		if err := m.transfer.CreateSnapshot(ctx, virtualMachineID, name); err != nil {
			return SourceSnapshot{}, err
		}
		record.Sequence = final
		record.FinalSequence = final
		if err := m.store.writeSource(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	return m.describeSnapshot(ctx, virtualMachineID, migrationID, record.FinalSequence)
}

// SendSourceStream applies the disk limit and streams a requested snapshot.
func (m *VMMigration) SendSourceStream(ctx context.Context, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return 0, err
	}
	if sequence != record.Sequence {
		return 0, vm.ErrConflict
	}
	if err := m.applySourceDiskLimit(ctx, migrationID, virtualMachineID, throughputMiBps); err != nil {
		return 0, err
	}

	streamContext, handle := m.beginSourceStream(ctx, virtualMachineID)
	defer m.endSourceStream(virtualMachineID, handle)

	name := migrationSnapshotName(migrationID, sequence)
	base := ""
	if sequence > 1 {
		base = migrationSnapshotName(migrationID, sequence-1)
	}
	return m.transfer.SendSnapshot(streamContext, virtualMachineID, name, base, resumeToken, w)
}

// beginSourceStream registers an in-flight source stream and returns a context
// that an unlock or a daemon shutdown cancels. It stops any earlier stream for
// the same VM so at most one stream holds the snapshot.
func (m *VMMigration) beginSourceStream(parent context.Context, virtualMachineID string) (context.Context, *transferHandle) {
	streamContext, cancel := context.WithCancel(parent)
	handle := &transferHandle{cancel: cancel, done: make(chan struct{})}

	m.sourceStreamsMutex.Lock()
	if previous := m.sourceStreams[virtualMachineID]; previous != nil {
		previous.cancel()
	}
	m.sourceStreams[virtualMachineID] = handle
	m.sourceStreamsMutex.Unlock()

	return streamContext, handle
}

// endSourceStream clears one stream handle and signals its exit.
func (m *VMMigration) endSourceStream(virtualMachineID string, handle *transferHandle) {
	m.sourceStreamsMutex.Lock()
	if m.sourceStreams[virtualMachineID] == handle {
		delete(m.sourceStreams, virtualMachineID)
	}
	m.sourceStreamsMutex.Unlock()

	handle.cancel()
	close(handle.done)
}

// stopSourceStream cancels an in-flight source stream for a VM and waits for it
// to exit. A later snapshot destroy then cannot fail on a busy dataset.
func (m *VMMigration) stopSourceStream(ctx context.Context, virtualMachineID string) error {
	m.sourceStreamsMutex.Lock()
	handle := m.sourceStreams[virtualMachineID]
	m.sourceStreamsMutex.Unlock()
	if handle == nil {
		return nil
	}

	handle.cancel()
	select {
	case <-handle.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for source stream: %w", ctx.Err())
	}
}

// applySourceDiskLimit saves a stream limit under the VM lock, then releases it.
func (m *VMMigration) applySourceDiskLimit(ctx context.Context, migrationID, virtualMachineID string, throughputMiBps int) error {
	if throughputMiBps <= 0 {
		return nil
	}
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return err
	}
	applied, err := m.machines.LimitSourceDisk(ctx, virtualMachineID, throughputMiBps)
	if err != nil {
		return err
	}
	if applied == 0 || applied == record.TemporaryDiskLimitMiBps {
		return nil
	}
	record.TemporaryDiskLimitMiBps = applied
	return m.store.writeSource(record)
}

// describeSnapshot returns the sequence, estimated size, and GUID of one snapshot.
func (m *VMMigration) describeSnapshot(ctx context.Context, virtualMachineID, migrationID string, sequence int) (SourceSnapshot, error) {
	name := migrationSnapshotName(migrationID, sequence)
	base := ""
	if sequence > 1 {
		base = migrationSnapshotName(migrationID, sequence-1)
	}
	sizeBytes, err := m.transfer.EstimateStreamBytes(ctx, virtualMachineID, name, base)
	if err != nil {
		return SourceSnapshot{}, err
	}
	guid, err := m.transfer.SnapshotGUID(ctx, virtualMachineID, name)
	if err != nil {
		return SourceSnapshot{}, err
	}
	return SourceSnapshot{Sequence: sequence, SizeBytes: sizeBytes, GUID: guid}, nil
}

// boundSourceRecord verifies the migration before returning the source record.
func (m *VMMigration) boundSourceRecord(virtualMachineID, migrationID string) (SourceMigrationRecord, error) {
	record, err := m.store.readSource(virtualMachineID)
	if err != nil {
		return SourceMigrationRecord{}, err
	}
	if record.ID != migrationID {
		return SourceMigrationRecord{}, vm.ErrConflict
	}
	return record, nil
}

// sourceHandshake reads the current VM records into the portable response.
func (m *VMMigration) sourceHandshake(virtualMachineID string) (SourceHandshake, error) {
	desired, err := m.machines.ReadDesired(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	observed, err := m.machines.ReadObserved(virtualMachineID)
	if err != nil {
		return SourceHandshake{}, err
	}
	return SourceHandshake{
		Config: PortableConfig{
			VirtualMachineID:        desired.ID,
			CreateFingerprint:       desired.CreateFingerprint,
			Generation:              desired.Generation,
			SpecificationGeneration: desired.SpecificationGeneration,
			RestartGeneration:       desired.RestartGeneration,
			DesiredState:            desired.State,
			Specification:           vm.CloneSpecification(desired.Specification),
		},
		ObservedState: observed.State,
	}, nil
}
