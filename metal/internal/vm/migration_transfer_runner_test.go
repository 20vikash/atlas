package vm

import (
	"context"
	"errors"
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
	// The queue holds one snapshot, so the copy loop stops after the full interval.
	source.nextQueue = []SourceSnapshot{{Sequence: 1, SizeBytes: 1000, GUID: "g"}}
	source.streamBytes = 1000
	transfer.guid = "g"
	store := writeCopyingTarget(t, machines, StateRunning)

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
	source.nextQueue = []SourceSnapshot{{Sequence: 1, SizeBytes: 1000, GUID: "source-guid"}}
	transfer.guid = "different-guid"
	store := writeCopyingTarget(t, machines, StateRunning)

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
	source.nextQueue = []SourceSnapshot{{Sequence: 1, SizeBytes: 1000, GUID: "g"}}
	store := writeCopyingTarget(t, machines, StateRunning)

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

func TestRunTransferCutsOverAtTheIntervalLimit(t *testing.T) {
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
	// Fail the source stop, so the run halts in the stopping phase for inspection.
	source.stopError = errors.New("stop refused")

	migrationManager.runTransfer(context.Background(), "vm-1")

	if source.nextCalls != 0 {
		t.Fatalf("asked the source past the interval limit: %d calls", source.nextCalls)
	}
	if source.stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", source.stopCalls)
	}
	record, err = store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != PhaseStopping {
		t.Fatalf("phase = %s, want stopping", record.Phase)
	}
}

func TestRunTransferThrottlesThenCutsOverToReady(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	migrationManager.settings.FinalDeltaMiB = 1
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.guid = "g"
	source.streamBytes = 10
	large := int64(64 * 1024 * 1024)
	source.nextQueue = []SourceSnapshot{
		{Sequence: 1, SizeBytes: large, GUID: "g"},
		{Sequence: 2, SizeBytes: large, GUID: "g"},
		{Sequence: 3, SizeBytes: 512, GUID: "g"},
	}
	source.stopSnapshot = SourceSnapshot{Sequence: 3, SizeBytes: 512, GUID: "g"}
	seedTargetVM(t, machines, StateRunning)
	store := writeCopyingTarget(t, machines, StateRunning)
	runtime := machines.runtime.(*fakeRuntime)
	runtime.state = StateStopped

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationReady {
		t.Fatalf("status = %s (%+v)", record.Status, record.Error)
	}
	// The first full interval is unthrottled, the large delta uses 64 MiB/s, and
	// the final snapshot after stop is unthrottled.
	if want := []int{0, 64, 0}; !equalInts(source.streamMiBps, want) {
		t.Fatalf("throttle steps = %v", source.streamMiBps)
	}
	if source.stopCalls != 1 || runtime.coldStarts != 1 {
		t.Fatalf("stop calls = %d, cold starts = %d", source.stopCalls, runtime.coldStarts)
	}
	if !record.TargetNetworkReady || !record.TargetStateApplied {
		t.Fatalf("checkpoints = %+v", record)
	}
}

func TestRunTransferStopsANonRunningSourceFirst(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.guid = "g"
	source.streamBytes = 10
	source.stopSnapshot = SourceSnapshot{Sequence: 1, SizeBytes: 4096, GUID: "g"}
	seedTargetVM(t, machines, StateStopped)
	store := writeCopyingTarget(t, machines, StateStopped)
	runtime := machines.runtime.(*fakeRuntime)
	runtime.state = StateStopped

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationReady {
		t.Fatalf("status = %s (%+v)", record.Status, record.Error)
	}
	if source.nextCalls != 0 || source.stopCalls != 1 {
		t.Fatalf("next calls = %d, stop calls = %d", source.nextCalls, source.stopCalls)
	}
	if runtime.coldStarts != 0 {
		t.Fatalf("cold starts = %d, want 0 for a stopped target", runtime.coldStarts)
	}
	if len(record.Intervals) != 1 || !record.Intervals[0].Completed {
		t.Fatalf("intervals = %+v", record.Intervals)
	}
}

func TestRunTransferKeepsLockedOnTargetStartFailure(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.guid = "g"
	source.streamBytes = 10
	source.stopSnapshot = SourceSnapshot{Sequence: 1, SizeBytes: 4096, GUID: "g"}
	seedTargetVM(t, machines, StateRunning)
	store := writeCopyingTarget(t, machines, StateStopped)
	runtime := machines.runtime.(*fakeRuntime)
	runtime.state = StateStopped
	runtime.coldStartError = errors.New("boot failed")

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationFailed {
		t.Fatalf("status = %s, want failed", record.Status)
	}
	// A failure after source stop must not unlock the source.
	if source.removeCalls != 0 {
		t.Fatalf("remove calls = %d, want the source kept locked", source.removeCalls)
	}
}

func TestAdvanceTargetResumesACopyingTransfer(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	source.nextQueue = []SourceSnapshot{{Sequence: 1, SizeBytes: 800, GUID: "g"}}
	source.streamBytes = 800
	transfer.guid = "g"
	store := writeCopyingTarget(t, machines, StateRunning)

	// A reconcile pass over a copying migration resumes the transfer.
	if err := migrationManager.AdvanceTarget(context.Background(), "vm-1"); err != nil {
		t.Fatal(err)
	}
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

func TestStartTransferRunsOnceAndShutdownWaits(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	source.nextQueue = []SourceSnapshot{{Sequence: 1, SizeBytes: 500, GUID: "g"}}
	source.streamBytes = 500
	transfer.guid = "g"
	store := writeCopyingTarget(t, machines, StateRunning)

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

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
