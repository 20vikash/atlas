package vm

import (
	"context"
	"testing"
	"time"
)

// writeCopyingTarget sets up a target record in the copying phase with a token.
func writeCopyingTarget(t *testing.T, machines *Manager, sourceState State) *migrationStore {
	t.Helper()
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	record := TargetMigrationRecord{
		ID:                  "mig-1",
		VirtualMachineID:    "vm-1",
		Source:              "http://10.0.0.3:9000",
		Status:              MigrationRunning,
		Phase:               PhaseCopying,
		SourceObservedState: sourceState,
		CopyStartedAt:       time.Now().UTC(),
	}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	if err := store.writeToken("vm-1", "tok-1"); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRunTransferCopiesAFullInterval(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	source.nextSnapshot = SourceSnapshot{Sequence: 1, SizeBytes: 1000, GUID: "g"}
	source.streamBytes = 1000
	transfer.guid = "g"
	store := writeCopyingTarget(t, machines, StateStopped)

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationRunning || record.Phase != PhaseCopying {
		t.Fatalf("record = %+v", record)
	}
	if len(record.Intervals) != 1 || !record.Intervals[0].Completed || record.Intervals[0].BytesTransferred != 1000 {
		t.Fatalf("intervals = %+v", record.Intervals)
	}
	if transfer.received != 1 {
		t.Fatalf("received count = %d", transfer.received)
	}
}

func TestRunTransferFailsOnGUIDMismatch(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	source.nextSnapshot = SourceSnapshot{Sequence: 1, SizeBytes: 1000, GUID: "source-guid"}
	transfer.guid = "different-guid"
	store := writeCopyingTarget(t, machines, StateStopped)

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationFailed || record.Error == nil {
		t.Fatalf("record = %+v", record)
	}
}

func TestRunTransferRefusesAnUnrelatedDataset(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.datasetExists = true
	source.nextSnapshot = SourceSnapshot{Sequence: 1, SizeBytes: 1000, GUID: "g"}
	store := writeCopyingTarget(t, machines, StateStopped)

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationFailed {
		t.Fatalf("status = %s, want failed", record.Status)
	}
	if transfer.received != 0 {
		t.Fatal("received a stream into an unrelated dataset")
	}
}

func TestRunTransferStopsAtTheIntervalLimit(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	store := writeCopyingTarget(t, machines, StateRunning)
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	for sequence := 1; sequence <= maxTransferIntervals; sequence++ {
		record.Intervals = append(record.Intervals, IntervalProgress{Sequence: sequence, Completed: true})
	}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	if source.nextCalls != 0 {
		t.Fatalf("asked the source past the interval limit: %d calls", source.nextCalls)
	}
}

func TestStartTransferRunsOnceAndShutdownWaits(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	source.nextSnapshot = SourceSnapshot{Sequence: 1, SizeBytes: 500, GUID: "g"}
	source.streamBytes = 500
	transfer.guid = "g"
	store := writeCopyingTarget(t, machines, StateStopped)

	migrationManager.StartTransfer("vm-1")
	migrationManager.StartTransfer("vm-1")
	if err := migrationManager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Intervals) != 1 || !record.Intervals[0].Completed {
		t.Fatalf("intervals = %+v", record.Intervals)
	}
}
