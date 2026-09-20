package vmmigration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
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

// sourceIdleTimeout follows Atlas's 10-minute target visibility timeout.
const sourceIdleTimeout = 15 * time.Minute

// writeSourceContact records the latest source control call.
func (m *VMMigration) writeSourceContact(record SourceMigrationRecord) error {
	record.LastContactAt = m.now()
	return m.store.writeSource(record)
}

// hasSourceStream reports whether this VM is sending snapshot bytes right now.
func (m *VMMigration) hasSourceStream(virtualMachineID string) bool {
	m.sourceStreamsMutex.Lock()
	defer m.sourceStreamsMutex.Unlock()
	return m.sourceStreams[virtualMachineID] != nil
}

// isSourceExpirable reports whether the host can safely release the source lock.
// A stopped source can have a running target, so it always needs an operator.
func (m *VMMigration) isSourceExpirable(record SourceMigrationRecord) bool {
	if record.Expired || record.Stopped {
		return false
	}
	if m.hasSourceStream(record.VirtualMachineID) {
		return false
	}
	return m.now().Sub(record.LastContactAt) > sourceIdleTimeout
}

// ExpiredSourceVirtualMachineIDs returns the VMs whose source lock can be released.
func (m *VMMigration) ExpiredSourceVirtualMachineIDs(_ context.Context) ([]string, error) {
	virtualMachineIDs, err := m.store.listVirtualMachineIDs()
	if err != nil {
		return nil, err
	}
	expirable := make([]string, 0, len(virtualMachineIDs))
	for _, virtualMachineID := range virtualMachineIDs {
		if !m.store.has(m.store.sourcePath(virtualMachineID)) {
			continue
		}
		record, err := m.store.readSource(virtualMachineID)
		if err != nil {
			return nil, err
		}
		if m.isSourceExpirable(record) {
			expirable = append(expirable, virtualMachineID)
		}
	}
	return expirable, nil
}

// ExpireSource releases an idle pre-stop source lock and leaves a tombstone.
func (m *VMMigration) ExpireSource(ctx context.Context, virtualMachineID string) error {
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read under the lock because StopSource uses the same lock.
	record, err := m.store.readSource(virtualMachineID)
	if errors.Is(err, vm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !m.isSourceExpirable(record) {
		return nil
	}

	if err := m.removeMigrationSnapshots(ctx, record); err != nil {
		return err
	}
	if record.TemporaryDiskLimitMiBps > 0 {
		if err := m.machines.RefreshSourceDisk(ctx, virtualMachineID); err != nil {
			return err
		}
	}

	record.Expired = true
	if err := m.store.writeSource(record); err != nil {
		return err
	}
	m.logger.Warn("released an idle migration source lock",
		"migration_id", record.ID, "virtual_machine_id", virtualMachineID,
		"idle_seconds", int(m.now().Sub(record.LastContactAt).Seconds()))
	return nil
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
		// A new migration replaces an expired lock. Its old migration cannot revive it.
		if !existing.Expired || existing.ID == migrationID {
			if existing.Expired || existing.ID != migrationID {
				return SourceHandshake{}, vm.ErrConflict
			}
			if err := m.writeSourceContact(existing); err != nil {
				return SourceHandshake{}, err
			}
			return m.sourceHandshake(virtualMachineID)
		}
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
	}
	if err := m.writeSourceContact(record); err != nil {
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
	return m.writeSourceContact(record)
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
		if err := m.writeSourceContact(record); err != nil {
			return err
		}
	}
	if !record.DestroyStorageComplete {
		if err := m.machines.ReleaseStorage(ctx, virtualMachineID); err != nil {
			return err
		}
		record.DestroyStorageComplete = true
		if err := m.writeSourceContact(record); err != nil {
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
	// A repeat of the same request changes nothing else, so stamp the contact
	// here rather than only on the paths that advance the sequence.
	if err := m.writeSourceContact(record); err != nil {
		return SourceSnapshot{}, err
	}

	if receivedSequence > record.AcknowledgedSequence {
		record.AcknowledgedSequence = receivedSequence
		if err := m.writeSourceContact(record); err != nil {
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
		if err := m.writeSourceContact(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	return m.describeSnapshot(ctx, virtualMachineID, migrationID, record.Sequence)
}

// StopSource records the last received sequence, stops the source, and creates
// the final snapshot. Checkpoints make repeats safe.
func (m *VMMigration) StopSource(ctx context.Context, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error) {
	unlock, err := m.machines.LockOperation(ctx, virtualMachineID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	defer unlock()

	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return SourceSnapshot{}, err
	}
	if receivedSequence > record.Sequence {
		return SourceSnapshot{}, vm.ErrConflict
	}

	if !record.Stopped {
		if err := m.machines.NormalizeSourceToStopped(ctx, virtualMachineID); err != nil {
			return SourceSnapshot{}, err
		}
		record.Stopped = true
		if err := m.writeSourceContact(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	if !record.NetworkRemoved {
		if err := m.machines.RemoveMigrationNetwork(ctx, virtualMachineID); err != nil {
			return SourceSnapshot{}, err
		}
		record.NetworkRemoved = true
		if err := m.writeSourceContact(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	if record.FinalSequence == 0 {
		final := max(record.AcknowledgedSequence, receivedSequence) + 1
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
		if err := m.writeSourceContact(record); err != nil {
			return SourceSnapshot{}, err
		}
	}
	return m.describeSnapshot(ctx, virtualMachineID, migrationID, record.FinalSequence)
}

// StartSourceStream starts one mutual-TLS listener that sends the requested ZFS stream.
func (m *VMMigration) StartSourceStream(ctx context.Context, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int) error {
	record, err := m.boundSourceRecord(virtualMachineID, migrationID)
	if err != nil {
		return err
	}
	if err := m.writeSourceContact(record); err != nil {
		return err
	}
	if sequence != record.Sequence {
		return vm.ErrConflict
	}
	m.transfersMutex.Lock()
	if m.closed {
		m.transfersMutex.Unlock()
		return vm.ErrConflict
	}
	m.sourceStreamsWaitGroup.Add(1)
	m.transfersMutex.Unlock()
	waitRegistered := true
	defer func() {
		if waitRegistered {
			m.sourceStreamsWaitGroup.Done()
		}
	}()

	m.sourceStreamsMutex.Lock()
	if len(m.sourceStreams) > 0 {
		m.sourceStreamsMutex.Unlock()
		return vm.ErrConflict
	}

	// The stream outlives its request, so a target that never connects cannot hold the port.
	streamContext, cancel := context.WithTimeout(m.rootContext, maxTransferDuration)
	handle := &transferHandle{cancel: cancel, done: make(chan struct{})}
	m.sourceStreams[virtualMachineID] = handle
	m.sourceStreamsMutex.Unlock()
	if err := m.applySourceDiskLimit(ctx, migrationID, virtualMachineID, throughputMiBps); err != nil {
		m.endSourceStream(virtualMachineID, handle)
		return err
	}

	name := migrationSnapshotName(migrationID, sequence)
	base := ""
	if sequence > 1 {
		base = migrationSnapshotName(migrationID, sequence-1)
	}
	server, err := m.transfer.StartSnapshotServer(streamContext, virtualMachineID, name, base, resumeToken)
	if err != nil {
		m.endSourceStream(virtualMachineID, handle)
		return err
	}
	waitRegistered = false
	go func() {
		defer m.sourceStreamsWaitGroup.Done()
		if err := server.Wait(); err != nil && streamContext.Err() == nil {
			m.logger.Error("source snapshot stream failed", "migration_id", migrationID, "error", err)
		}
		m.endSourceStream(virtualMachineID, handle)
	}()
	return nil
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
	return m.writeSourceContact(record)
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
	if record.ID != migrationID || record.Expired {
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
