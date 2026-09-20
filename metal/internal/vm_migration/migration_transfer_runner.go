package vmmigration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

const (
	// maxTransferIntervals bounds incremental rounds before cutover.
	maxTransferIntervals = 16
	// maxTransferDuration bounds copy time before cutover.
	maxTransferDuration = 30 * time.Minute
)

// throttleSteps are source disk limits in MiB/s for large deltas.
var throttleSteps = []int{64, 32, 16, 8}

// errTransferFailed marks a definite failure that already updated the record.
var errTransferFailed = errors.New("migration transfer failed")

// transferHandle cancels one VM's background worker and signals its exit.
type transferHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartTransfer starts one worker per VM. A running worker is unchanged.
func (m *VMMigration) StartTransfer(virtualMachineID string) {
	m.transfersMutex.Lock()
	defer m.transfersMutex.Unlock()
	if m.closed {
		return
	}
	if _, active := m.transfers[virtualMachineID]; active {
		return
	}
	workerContext, cancel := context.WithCancel(m.rootContext)
	handle := &transferHandle{cancel: cancel, done: make(chan struct{})}
	m.transfers[virtualMachineID] = handle
	m.transfersWaitGroup.Add(1)
	go func() {
		defer m.transfersWaitGroup.Done()
		defer close(handle.done)
		defer m.forgetTransfer(virtualMachineID)
		m.runTransfer(workerContext, virtualMachineID)
	}()
}

// CancelTransfer stops a VM worker and waits for active streams to exit.
func (m *VMMigration) CancelTransfer(ctx context.Context, virtualMachineID string) error {
	m.transfersMutex.Lock()
	handle := m.transfers[virtualMachineID]
	m.transfersMutex.Unlock()
	if handle == nil {
		return nil
	}
	handle.cancel()
	select {
	case <-handle.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for migration worker: %w", ctx.Err())
	}
}

// Shutdown stops accepting workers and waits for the active ones to stop.
func (m *VMMigration) Shutdown(shutdownContext context.Context) error {
	m.transfersMutex.Lock()
	if !m.closed {
		m.closed = true
		m.rootCancel()
	}
	m.transfersMutex.Unlock()

	finished := make(chan struct{})
	go func() {
		m.transfersWaitGroup.Wait()
		m.sourceStreamsWaitGroup.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-shutdownContext.Done():
		return fmt.Errorf("wait for migration transfers: %w", shutdownContext.Err())
	}
}

// forgetTransfer clears the active-worker handle for a VM.
func (m *VMMigration) forgetTransfer(virtualMachineID string) {
	m.transfersMutex.Lock()
	delete(m.transfers, virtualMachineID)
	m.transfersMutex.Unlock()
}

// runTransfer advances one migration from copying to ready. Transient errors
// leave it for the next pass.
func (m *VMMigration) runTransfer(ctx context.Context, virtualMachineID string) {
	for {
		record, err := m.store.readTarget(virtualMachineID)
		if err != nil {
			return
		}

		var advanceError error
		switch {
		case isTerminalStatus(record.Status):
			return
		case record.AbortRequested:
			advanceError = m.advanceAbort(ctx, record)
		case record.FinishRequested && record.Status == MigrationReady:
			advanceError = m.advanceFinish(ctx, record)
		case record.Status == MigrationRunning && record.Phase == PhaseCopying:
			advanceError = m.advanceCopying(ctx, record)
		case record.Status == MigrationRunning && record.Phase == PhaseStopping:
			advanceError = m.advanceStopping(ctx, record)
		case record.Status == MigrationRunning && record.Phase == PhaseStarting:
			advanceError = m.advanceStarting(ctx, record)
		default:
			return
		}
		if advanceError != nil {
			if ctx.Err() == nil && !errors.Is(advanceError, errTransferFailed) {
				m.logger.Error("migration transfer interrupted", "migration_id", record.ID, "error", advanceError)
			}
			return
		}
	}
}

// advanceCopying copies an interval or enters stopping. Running sources send a
// full first interval; other sources cut over first.
func (m *VMMigration) advanceCopying(ctx context.Context, record TargetMigrationRecord) error {
	last := lastCompletedSequence(record)
	if last == 0 && record.SourceObservedState != vm.StateRunning {
		return m.enterStopping(ctx, record)
	}
	if last >= 1 && m.transferLimitReached(record) {
		return m.enterStopping(ctx, record)
	}

	snapshot, err := m.source.NextSnapshot(ctx, record.Source, record.ID, record.VirtualMachineID, last)
	if err != nil {
		return err
	}
	if snapshot.Sequence < 1 {
		return fmt.Errorf("source returned an invalid sequence %d", snapshot.Sequence)
	}
	if last >= 1 && m.isSmallDelta(snapshot) {
		return m.enterStopping(ctx, record)
	}
	return m.receiveSnapshot(ctx, record, snapshot, m.intervalThroughput(record))
}

// advanceStopping pulls and verifies the final snapshot, then enters starting.
func (m *VMMigration) advanceStopping(ctx context.Context, record TargetMigrationRecord) error {
	final, err := m.source.StopSource(
		ctx,
		record.Source,
		record.ID,
		record.VirtualMachineID,
		lastCompletedSequence(record),
	)
	if err != nil {
		return err
	}
	if final.Sequence < 1 {
		return m.failTransfer(ctx, record, "source returned an invalid final sequence")
	}

	record, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		target.SourceObservedState = vm.StateStopped
		target.SourceStopped = true
		target.FinalSequence = final.Sequence
	})
	if err != nil {
		return err
	}
	if err := m.receiveSnapshot(ctx, record, final, 0); err != nil {
		return err
	}

	_, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		target.Phase = PhaseStarting
	})
	return err
}

// advanceStarting creates the target network, applies VM state, and marks ready.
// A failure keeps both hosts locked for rollback.
func (m *VMMigration) advanceStarting(ctx context.Context, record TargetMigrationRecord) error {
	unlock, err := m.machines.LockOperation(ctx, record.VirtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	if !record.TargetNetworkReady {
		if err := m.machines.EnsureMigrationNetwork(ctx, record.VirtualMachineID); err != nil {
			if isWorkerCancelled(ctx, err) {
				return err
			}
			return m.failTransfer(ctx, record, "create target network: "+err.Error())
		}
		if _, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.TargetNetworkReady = true
		}); err != nil {
			return err
		}
	}
	if !record.TargetStateApplied {
		if err := m.machines.ApplyMigratedTargetState(ctx, record.VirtualMachineID); err != nil {
			if isWorkerCancelled(ctx, err) {
				return err
			}
			return m.failTransfer(ctx, record, "apply target state: "+err.Error())
		}
		if _, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
			target.TargetStateApplied = true
		}); err != nil {
			return err
		}
	}

	_, err = m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		target.Status = MigrationReady
	})
	return err
}

// enterStopping records the move from copying to the stopping phase.
func (m *VMMigration) enterStopping(ctx context.Context, record TargetMigrationRecord) error {
	_, err := m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		target.Phase = PhaseStopping
	})
	return err
}

// isSmallDelta reports whether the source can stop instead of copying.
func (m *VMMigration) isSmallDelta(snapshot SourceSnapshot) bool {
	return snapshot.SizeBytes <= int64(m.settings.FinalDeltaMiB)*1024*1024
}

// intervalThroughput returns the next source disk limit. The first interval is
// unlimited; each later delta lowers the limit one step.
func (m *VMMigration) intervalThroughput(record TargetMigrationRecord) int {
	if lastCompletedSequence(record) == 0 {
		return 0
	}
	throttled := 0
	for _, interval := range record.Intervals {
		if interval.Completed && interval.ThroughputMiBps > 0 {
			throttled++
		}
	}
	if throttled >= len(throttleSteps) {
		return throttleSteps[len(throttleSteps)-1]
	}
	return throttleSteps[throttled]
}

// receiveSnapshot pulls one snapshot into the target dataset and verifies it.
func (m *VMMigration) receiveSnapshot(ctx context.Context, record TargetMigrationRecord, snapshot SourceSnapshot, throughputMiBps int) error {
	resumeToken, err := m.transfer.ReceiveResumeToken(ctx, record.VirtualMachineID)
	if err != nil {
		return err
	}
	if resumeToken == "" && snapshot.Sequence == 1 {
		exists, err := m.transfer.TargetDatasetExists(ctx, record.VirtualMachineID)
		if err != nil {
			return err
		}
		if exists {
			return m.failTransfer(ctx, record, "unrelated target dataset already exists")
		}
	}

	record, err = m.beginInterval(ctx, record, snapshot, throughputMiBps)
	if err != nil {
		return err
	}
	estimatedBytes, err := m.streamInterval(ctx, record, snapshot, resumeToken, throughputMiBps)
	if err != nil {
		return err
	}

	receivedGUID, err := m.transfer.SnapshotGUID(ctx, record.VirtualMachineID, migrationSnapshotName(record.ID, snapshot.Sequence))
	if err != nil {
		return err
	}
	if receivedGUID != snapshot.GUID {
		return m.failTransfer(ctx, record, "received snapshot GUID does not match the source")
	}

	return m.completeInterval(ctx, record, snapshot.Sequence, estimatedBytes, receivedGUID)
}

// streamInterval pulls one snapshot and records its estimated size.
func (m *VMMigration) streamInterval(ctx context.Context, record TargetMigrationRecord, snapshot SourceSnapshot, resumeToken string, throughputMiBps int) (int64, error) {
	if err := m.source.StartSnapshotStream(
		ctx, record.Source, record.ID, record.VirtualMachineID,
		snapshot.Sequence, resumeToken, throughputMiBps,
	); err != nil {
		return 0, err
	}
	if err := m.transfer.ReceiveSnapshotTLS(ctx, record.VirtualMachineID, record.Source); err != nil {
		return 0, err
	}
	return snapshot.SizeBytes, nil
}

// beginInterval records the start of one interval and returns the saved record.
func (m *VMMigration) beginInterval(ctx context.Context, record TargetMigrationRecord, snapshot SourceSnapshot, throughputMiBps int) (TargetMigrationRecord, error) {
	return m.mutateTarget(ctx, record.VirtualMachineID, func(target *TargetMigrationRecord) {
		target.ActiveSequence = snapshot.Sequence
		if hasInterval(target.Intervals, snapshot.Sequence) {
			return
		}
		target.Intervals = append(target.Intervals, IntervalProgress{
			Sequence:        snapshot.Sequence,
			StartedAt:       m.now(),
			TotalBytes:      snapshot.SizeBytes,
			ThroughputMiBps: throughputMiBps,
		})
	})
}

// completeInterval marks one interval done with its byte count and GUID.
func (m *VMMigration) completeInterval(ctx context.Context, record TargetMigrationRecord, sequence int, bytesReceived int64, guid string) error {
	// The snapshot is already on the dataset. An unrecorded interval makes the
	// next pass ask for it again, which zfs receive then rejects.
	_, err := m.mutateTarget(context.WithoutCancel(ctx), record.VirtualMachineID, func(target *TargetMigrationRecord) {
		for index := range target.Intervals {
			if target.Intervals[index].Sequence != sequence {
				continue
			}
			interval := &target.Intervals[index]
			interval.BytesTransferred = bytesReceived
			interval.GUID = guid
			interval.Completed = true
			interval.DurationSeconds = int(m.now().Sub(interval.StartedAt).Seconds())
		}
	})
	return err
}

// isWorkerCancelled identifies a shutdown that leaves checkpointed work safe to retry.
func isWorkerCancelled(ctx context.Context, err error) bool {
	return ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

// failTransfer marks the migration failed and keeps the dataset and snapshots.
func (m *VMMigration) failTransfer(ctx context.Context, record TargetMigrationRecord, message string) error {
	// A cancelled worker must still leave the reason on the record.
	if _, err := m.mutateTarget(context.WithoutCancel(ctx), record.VirtualMachineID, func(target *TargetMigrationRecord) {
		target.Status = MigrationFailed
		target.Error = &vm.OperationError{Code: "transfer_failed", Message: message, UpdatedAt: m.now()}
	}); err != nil {
		return err
	}
	return errTransferFailed
}

// transferLimitReached reports whether the copy hit the interval or time limit.
func (m *VMMigration) transferLimitReached(record TargetMigrationRecord) bool {
	completed := 0
	for _, interval := range record.Intervals {
		if interval.Completed {
			completed++
		}
	}
	if completed >= maxTransferIntervals {
		return true
	}
	return !record.CopyStartedAt.IsZero() && m.now().Sub(record.CopyStartedAt) > maxTransferDuration
}

// lastCompletedSequence returns the highest completed interval sequence.
func lastCompletedSequence(record TargetMigrationRecord) int {
	last := 0
	for _, interval := range record.Intervals {
		if interval.Completed && interval.Sequence > last {
			last = interval.Sequence
		}
	}
	return last
}

// hasInterval reports whether a sequence already has an interval entry.
func hasInterval(intervals []IntervalProgress, sequence int) bool {
	for _, interval := range intervals {
		if interval.Sequence == sequence {
			return true
		}
	}
	return false
}
