package vm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// writeReadyTarget writes a ready target record for finish tests.
func writeReadyTarget(t *testing.T, machines *Manager, mutate func(*TargetMigrationRecord)) *migrationStore {
	t.Helper()
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	record := TargetMigrationRecord{
		ID:               "mig-1",
		VirtualMachineID: "vm-1",
		Source:           "http://10.0.0.3:9000",
		Status:           MigrationReady,
		Phase:            PhaseStarting,
		CreatedAt:        time.Now().UTC(),
	}
	if mutate != nil {
		mutate(&record)
	}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRunTransferFinishesToCompleted(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	store := writeReadyTarget(t, machines, func(r *TargetMigrationRecord) {
		r.FinishRequested = true
		r.FinalSequence = 2
		r.Intervals = []IntervalProgress{
			{Sequence: 1, Completed: true},
			{Sequence: 2, Completed: true},
		}
	})

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationCompleted || record.Phase != "" || record.FinishedAt.IsZero() {
		t.Fatalf("record = %+v", record)
	}
	if source.finishCalls != 1 {
		t.Fatalf("source finish calls = %d, want 1", source.finishCalls)
	}
	// Finish removes the received migration snapshots, so they do not pile up.
	if want := []string{"migration-mig-1-1", "migration-mig-1-2"}; !equalStringSlices(transfer.removed, want) {
		t.Fatalf("removed snapshots = %v", transfer.removed)
	}
}

func TestRequestFinishRecordsIntent(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	store := writeReadyTarget(t, machines, nil)

	if err := migrationManager.RequestFinish(context.Background(), "mig-1"); err != nil {
		t.Fatal(err)
	}
	record, _ := store.readTarget("vm-1")
	if !record.FinishRequested {
		t.Fatal("finish request was not recorded")
	}
}

func TestRequestFinishRequiresReady(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeCopyingTarget(t, machines, StateRunning)

	if err := migrationManager.RequestFinish(context.Background(), "mig-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish before ready = %v, want ErrConflict", err)
	}
}

func TestRequestFinishConflictsWithAbort(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeReadyTarget(t, machines, func(r *TargetMigrationRecord) { r.AbortRequested = true })

	if err := migrationManager.RequestFinish(context.Background(), "mig-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish after abort = %v, want ErrConflict", err)
	}
}

func TestRunTransferAbortsBeforeStop(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	store := writeCopyingTarget(t, machines, StateRunning)
	record, _ := store.readTarget("vm-1")
	record.AbortRequested = true
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	rec, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != MigrationAborted || rec.Phase != "" {
		t.Fatalf("record = %+v", rec)
	}
	// A source that never stopped is unlocked, not restarted.
	if source.startCalls != 0 || source.removeCalls != 1 {
		t.Fatalf("start calls = %d, remove calls = %d", source.startCalls, source.removeCalls)
	}
	if transfer.aborts != 1 {
		t.Fatalf("receive aborts = %d, want 1", transfer.aborts)
	}
}

func TestRunTransferAbortsAfterStop(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	store := writeCopyingTarget(t, machines, StateRunning)
	record, _ := store.readTarget("vm-1")
	record.SourceStopped = true
	record.FinalSequence = 2
	record.AbortRequested = true
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	rec, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != MigrationAborted {
		t.Fatalf("status = %s, want aborted", rec.Status)
	}
	// A stopped source is restored, then unlocked.
	if source.startCalls != 1 || source.removeCalls != 1 {
		t.Fatalf("start calls = %d, remove calls = %d", source.startCalls, source.removeCalls)
	}
}

func TestRunTransferKeepsLockedWhenRollbackFails(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	source.removeError = errors.New("source unreachable")
	store := writeCopyingTarget(t, machines, StateRunning)
	record, _ := store.readTarget("vm-1")
	record.AbortRequested = true
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	rec, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if isTerminalStatus(rec.Status) {
		t.Fatal("a failed rollback must keep the migration locked")
	}
	if !rec.AbortRequested {
		t.Fatal("the abort request must be preserved for a retry")
	}
}
