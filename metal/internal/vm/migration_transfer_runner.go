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
	// maxTransferIntervals bounds the incremental rounds before cutover.
	maxTransferIntervals = 16
	// maxTransferDuration bounds the copy time before cutover.
	maxTransferDuration = 30 * time.Minute
	// progressCheckpointInterval is how often the active byte count is saved.
	progressCheckpointInterval = 5 * time.Second
)

// errTransferFailed marks a definite failure that already updated the record.
var errTransferFailed = errors.New("migration transfer failed")

// StartTransfer runs one background disk transfer for a VM. A second call for a
// VM that already has a running transfer does nothing.
func (m *MigrationManager) StartTransfer(virtualMachineID string) {
	m.transfersMutex.Lock()
	defer m.transfersMutex.Unlock()
	if m.closed {
		return
	}
	if _, active := m.transfers[virtualMachineID]; active {
		return
	}
	m.transfers[virtualMachineID] = struct{}{}
	m.transfersWaitGroup.Add(1)
	go func() {
		defer m.transfersWaitGroup.Done()
		defer m.forgetTransfer(virtualMachineID)
		m.runTransfer(m.rootContext, virtualMachineID)
	}()
}

// Shutdown stops accepting transfers and waits for the active ones to stop.
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

// forgetTransfer clears the active-transfer marker for a VM.
func (m *MigrationManager) forgetTransfer(virtualMachineID string) {
	m.transfersMutex.Lock()
	delete(m.transfers, virtualMachineID)
	m.transfersMutex.Unlock()
}

// runTransfer copies intervals until the migration settles, stops at a limit,
// or an error stops it. A transient error leaves the migration to resume later.
func (m *MigrationManager) runTransfer(ctx context.Context, virtualMachineID string) {
	for {
		record, err := m.store.readTarget(virtualMachineID)
		if err != nil {
			return
		}
		if record.Status != MigrationRunning || record.Phase != PhaseCopying {
			return
		}
		if m.transferLimitReached(record) {
			return
		}

		stop, err := m.transferInterval(ctx, record)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, errTransferFailed) {
				m.logger.Error("migration transfer interrupted", "migration_id", record.ID, "error", err)
			}
			return
		}
		if stop {
			return
		}
	}
}

// transferInterval pulls one snapshot into the target dataset and verifies it.
// A stopped source needs one full interval, so it returns stop true.
func (m *MigrationManager) transferInterval(ctx context.Context, record TargetMigrationRecord) (bool, error) {
	token, err := m.store.readToken(record.VirtualMachineID)
	if err != nil {
		return false, err
	}
	snapshot, err := m.source.NextSnapshot(ctx, record.Source, record.ID, token, lastCompletedSequence(record))
	if err != nil {
		return false, err
	}
	if snapshot.Sequence < 1 {
		return false, fmt.Errorf("source returned an invalid sequence %d", snapshot.Sequence)
	}

	resumeToken, err := m.transfer.ReceiveResumeToken(ctx, record.VirtualMachineID)
	if err != nil {
		return false, err
	}
	if resumeToken == "" && snapshot.Sequence == 1 {
		exists, err := m.transfer.TargetDatasetExists(ctx, record.VirtualMachineID)
		if err != nil {
			return false, err
		}
		if exists {
			return false, m.failTransfer(record, "unrelated target dataset already exists")
		}
	}

	record = m.beginInterval(record, snapshot)
	received, err := m.streamInterval(ctx, record, token, snapshot, resumeToken)
	if err != nil {
		return false, err
	}

	receivedGUID, err := m.transfer.SnapshotGUID(ctx, record.VirtualMachineID, migrationSnapshotName(record.ID, snapshot.Sequence))
	if err != nil {
		return false, err
	}
	if receivedGUID != snapshot.GUID {
		return false, m.failTransfer(record, "received snapshot GUID does not match the source")
	}

	m.completeInterval(record, snapshot.Sequence, received, receivedGUID)
	return record.SourceObservedState == StateStopped, nil
}

// streamInterval streams the source snapshot into the target receive and reports
// the received byte count. It checkpoints active progress while it runs.
func (m *MigrationManager) streamInterval(ctx context.Context, record TargetMigrationRecord, token string, snapshot SourceSnapshot, resumeToken string) (int64, error) {
	var received atomic.Int64
	reader, writer := io.Pipe()
	streamContext, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	go func() {
		_, sendError := m.source.StreamSnapshot(streamContext, record.Source, record.ID, token, snapshot.Sequence, resumeToken, writer)
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
func (m *MigrationManager) beginInterval(record TargetMigrationRecord, snapshot SourceSnapshot) TargetMigrationRecord {
	record.ActiveSequence = snapshot.Sequence
	if !hasInterval(record.Intervals, snapshot.Sequence) {
		record.Intervals = append(record.Intervals, IntervalProgress{
			Sequence:   snapshot.Sequence,
			StartedAt:  m.now(),
			TotalBytes: snapshot.SizeBytes,
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
