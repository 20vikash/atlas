package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

const (
	// maxTransferIntervals bounds incremental rounds before cutover.
	maxTransferIntervals = 16
	// maxTransferDuration bounds copy time before cutover.
	maxTransferDuration = 30 * time.Minute
	// progressCheckpointInterval controls active-byte checkpoints.
	progressCheckpointInterval = 5 * time.Second
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
func (m *MigrationManager) StartTransfer(virtualMachineID string) {
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
func (m *MigrationManager) CancelTransfer(ctx context.Context, virtualMachineID string) error {
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
func (m *MigrationManager) Shutdown(shutdownContext context.Context) error {
	m.transfersMutex.Lock()
	if !m.closed {
		m.closed = true
		m.rootCancel()
	}
	m.transfersMutex.Unlock()

	finished := make(chan struct{})
	go func() {
		m.transfersWaitGroup.Wait()
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
func (m *MigrationManager) forgetTransfer(virtualMachineID string) {
	m.transfersMutex.Lock()
	delete(m.transfers, virtualMachineID)
	m.transfersMutex.Unlock()
}

// runTransfer advances one migration from copying to ready. Transient errors
// leave it for the next pass.
func (m *MigrationManager) runTransfer(ctx context.Context, virtualMachineID string) {
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
func (m *MigrationManager) advanceCopying(ctx context.Context, record TargetMigrationRecord) error {
	last := lastCompletedSequence(record)
	if last == 0 && record.SourceObservedState != StateRunning {
		return m.enterStopping(record)
	}
	if last >= 1 && m.transferLimitReached(record) {
		return m.enterStopping(record)
	}

	snapshot, err := m.source.NextSnapshot(ctx, record.Source, record.ID, record.VirtualMachineID, last)
	if err != nil {
		return err
	}
	if snapshot.Sequence < 1 {
		return fmt.Errorf("source returned an invalid sequence %d", snapshot.Sequence)
	}
	if last >= 1 && m.isSmallDelta(snapshot) {
		return m.enterStopping(record)
	}
	return m.receiveSnapshot(ctx, record, snapshot, m.intervalThroughput(record))
}

// advanceStopping pulls and verifies the final snapshot, then enters starting.
func (m *MigrationManager) advanceStopping(ctx context.Context, record TargetMigrationRecord) error {
	final, err := m.source.StopSource(ctx, record.Source, record.ID, record.VirtualMachineID)
	if err != nil {
		return err
	}
	if final.Sequence < 1 {
		return m.failTransfer(record, "source returned an invalid final sequence")
	}

	record.SourceObservedState = StateStopped
	record.SourceStopped = true
	record.FinalSequence = final.Sequence
	if err := m.store.writeTarget(record); err != nil {
		return err
	}
	if err := m.receiveSnapshot(ctx, record, final, 0); err != nil {
		return err
	}

	current, err := m.store.readTarget(record.VirtualMachineID)
	if err != nil {
		return err
	}
	current.Phase = PhaseStarting
	return m.store.writeTarget(current)
}

// advanceStarting creates the target network, applies VM state, and marks ready.
// A failure keeps both hosts locked for rollback.
func (m *MigrationManager) advanceStarting(ctx context.Context, record TargetMigrationRecord) error {
	unlock, err := m.machines.operationLocks.lock(ctx, record.VirtualMachineID)
	if err != nil {
		return err
	}
	defer unlock()

	if !record.TargetNetworkReady {
		if err := m.machines.EnsureMigrationNetwork(ctx, record.VirtualMachineID); err != nil {
			return m.failTransfer(record, "create target network: "+err.Error())
		}
		record.TargetNetworkReady = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}
	if !record.TargetStateApplied {
		if err := m.machines.ApplyMigratedTargetState(ctx, record.VirtualMachineID); err != nil {
			return m.failTransfer(record, "apply target state: "+err.Error())
		}
		record.TargetStateApplied = true
		if err := m.store.writeTarget(record); err != nil {
			return err
		}
	}

	record.Status = MigrationReady
	return m.store.writeTarget(record)
}

// enterStopping records the move from copying to the stopping phase.
func (m *MigrationManager) enterStopping(record TargetMigrationRecord) error {
	record.Phase = PhaseStopping
	return m.store.writeTarget(record)
}

// isSmallDelta reports whether the source can stop instead of copying.
func (m *MigrationManager) isSmallDelta(snapshot SourceSnapshot) bool {
	return snapshot.SizeBytes <= int64(m.settings.FinalDeltaMiB)*1024*1024
}

// intervalThroughput returns the next source disk limit. The first interval is
// unlimited; each later delta lowers the limit one step.
func (m *MigrationManager) intervalThroughput(record TargetMigrationRecord) int {
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
func (m *MigrationManager) receiveSnapshot(ctx context.Context, record TargetMigrationRecord, snapshot SourceSnapshot, throughputMiBps int) error {
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
			return m.failTransfer(record, "unrelated target dataset already exists")
		}
	}

	record = m.beginInterval(record, snapshot, throughputMiBps)
	received, err := m.streamInterval(ctx, record, snapshot, resumeToken, throughputMiBps)
	if err != nil {
		return err
	}

	receivedGUID, err := m.transfer.SnapshotGUID(ctx, record.VirtualMachineID, migrationSnapshotName(record.ID, snapshot.Sequence))
	if err != nil {
		return err
	}
	if receivedGUID != snapshot.GUID {
		return m.failTransfer(record, "received snapshot GUID does not match the source")
	}

	m.completeInterval(record, snapshot.Sequence, received, receivedGUID)
	return nil
}

// streamInterval streams one snapshot and checkpoints received bytes.
func (m *MigrationManager) streamInterval(ctx context.Context, record TargetMigrationRecord, snapshot SourceSnapshot, resumeToken string, throughputMiBps int) (int64, error) {
	var received atomic.Int64
	reader, writer := io.Pipe()
	streamContext, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	go func() {
		_, sendError := m.source.StreamSnapshot(streamContext, record.Source, record.ID, record.VirtualMachineID, snapshot.Sequence, resumeToken, throughputMiBps, writer)
		writer.CloseWithError(sendError)
	}()

	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		m.checkpointProgress(streamContext, record.VirtualMachineID, snapshot.Sequence, &received)
	}()

	err := m.transfer.ReceiveSnapshot(ctx, record.VirtualMachineID, &countingReader{reader: reader, count: &received})
	cancelStream()
	<-progressDone
	return received.Load(), err
}

// checkpointProgress saves the active interval byte count until the stream ends.
func (m *MigrationManager) checkpointProgress(ctx context.Context, virtualMachineID string, sequence int, received *atomic.Int64) {
	ticker := time.NewTicker(progressCheckpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.saveIntervalBytes(virtualMachineID, sequence, received.Load())
		}
	}
}

// saveIntervalBytes records the bytes received for the active interval.
func (m *MigrationManager) saveIntervalBytes(virtualMachineID string, sequence int, bytesReceived int64) {
	record, err := m.store.readTarget(virtualMachineID)
	if err != nil {
		return
	}
	for index := range record.Intervals {
		if record.Intervals[index].Sequence == sequence && !record.Intervals[index].Completed {
			record.Intervals[index].BytesTransferred = bytesReceived
			_ = m.store.writeTarget(record)
			return
		}
	}
}

// beginInterval records the start of one interval and returns the saved record.
func (m *MigrationManager) beginInterval(record TargetMigrationRecord, snapshot SourceSnapshot, throughputMiBps int) TargetMigrationRecord {
	record.ActiveSequence = snapshot.Sequence
	if !hasInterval(record.Intervals, snapshot.Sequence) {
		record.Intervals = append(record.Intervals, IntervalProgress{
			Sequence:        snapshot.Sequence,
			StartedAt:       m.now(),
			TotalBytes:      snapshot.SizeBytes,
			ThroughputMiBps: throughputMiBps,
		})
	}
	_ = m.store.writeTarget(record)
	return record
}

// completeInterval marks one interval done with its byte count and GUID.
func (m *MigrationManager) completeInterval(record TargetMigrationRecord, sequence int, bytesReceived int64, guid string) {
	current, err := m.store.readTarget(record.VirtualMachineID)
	if err != nil {
		return
	}
	for index := range current.Intervals {
		if current.Intervals[index].Sequence == sequence {
			interval := &current.Intervals[index]
			interval.BytesTransferred = bytesReceived
			interval.GUID = guid
			interval.Completed = true
			interval.DurationSeconds = int(m.now().Sub(interval.StartedAt).Seconds())
		}
	}
	_ = m.store.writeTarget(current)
}

// failTransfer marks the migration failed and keeps the dataset and snapshots.
func (m *MigrationManager) failTransfer(record TargetMigrationRecord, message string) error {
	current, err := m.store.readTarget(record.VirtualMachineID)
	if err != nil {
		return err
	}
	current.Status = MigrationFailed
	current.Error = &OperationError{Code: "transfer_failed", Message: message, UpdatedAt: m.now()}
	if err := m.store.writeTarget(current); err != nil {
		return err
	}
	return errTransferFailed
}

// transferLimitReached reports whether the copy hit the interval or time limit.
func (m *MigrationManager) transferLimitReached(record TargetMigrationRecord) bool {
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

// countingReader counts the bytes read through it.
type countingReader struct {
	reader io.Reader
	count  *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.count.Add(int64(n))
	return n, err
}
