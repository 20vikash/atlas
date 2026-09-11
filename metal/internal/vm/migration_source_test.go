package vm

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// writeSourceRecord sets up a source lock for transfer tests.
func writeSourceRecord(t *testing.T, machines *Manager) {
	t.Helper()
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	record := SourceMigrationRecord{ID: "mig-1", VirtualMachineID: "vm-1", Caller: "metal-1"}
	if err := store.writeSource(record); err != nil {
		t.Fatal(err)
	}
}

func TestNextSourceSnapshotWalksSequences(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.guid = "g"
	transfer.sizeBytes = 1000
	writeSourceRecord(t, machines)
	ctx := context.Background()

	first, err := migrationManager.NextSourceSnapshot(ctx, "mig-1", "vm-1", "metal-1", 0)
	if err != nil || first.Sequence != 1 || first.SizeBytes != 1000 || first.GUID != "g" {
		t.Fatalf("first = %+v, %v", first, err)
	}

	second, err := migrationManager.NextSourceSnapshot(ctx, "mig-1", "vm-1", "metal-1", 1)
	if err != nil || second.Sequence != 2 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	third, err := migrationManager.NextSourceSnapshot(ctx, "mig-1", "vm-1", "metal-1", 2)
	if err != nil || third.Sequence != 3 {
		t.Fatalf("third = %+v, %v", third, err)
	}
	if want := []string{"migration-mig-1-1", "migration-mig-1-2", "migration-mig-1-3"}; !equalStringSlices(transfer.created, want) {
		t.Fatalf("created = %v", transfer.created)
	}
	// Acknowledging 2 removes 1 and keeps 2 as the base.
	if want := []string{"migration-mig-1-1"}; !equalStringSlices(transfer.removed, want) {
		t.Fatalf("removed = %v", transfer.removed)
	}
}

func TestNextSourceSnapshotIsIdempotent(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	writeSourceRecord(t, machines)
	ctx := context.Background()

	if _, err := migrationManager.NextSourceSnapshot(ctx, "mig-1", "vm-1", "metal-1", 0); err != nil {
		t.Fatal(err)
	}
	created := len(transfer.created)
	repeat, err := migrationManager.NextSourceSnapshot(ctx, "mig-1", "vm-1", "metal-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Sequence != 1 || len(transfer.created) != created {
		t.Fatalf("repeat = %+v, created %d", repeat, len(transfer.created))
	}
}

func TestNextSourceSnapshotRejectsAnotherMigration(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeSourceRecord(t, machines)

	if _, err := migrationManager.NextSourceSnapshot(context.Background(), "mig-2", "vm-1", "metal-1", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("other migration = %v, want ErrConflict", err)
	}
}

func TestSendSourceStreamRejectsAnUnknownSequence(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.sentBytes = 2048
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	if err := store.writeSource(SourceMigrationRecord{ID: "mig-1", VirtualMachineID: "vm-1", Caller: "metal-1", Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	var buffer bytes.Buffer
	written, err := migrationManager.SendSourceStream(ctx, "mig-1", "vm-1", "metal-1", 2, "", 0, &buffer)
	if err != nil || written != 2048 {
		t.Fatalf("send = %d, %v", written, err)
	}
	if want := []string{"migration-mig-1-2|migration-mig-1-1|"}; !equalStringSlices(transfer.sent, want) {
		t.Fatalf("sent = %v", transfer.sent)
	}
	if _, err := migrationManager.SendSourceStream(ctx, "mig-1", "vm-1", "metal-1", 3, "", 0, &buffer); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown sequence = %v, want ErrConflict", err)
	}
}

// seedSourceVM creates a source VM and lock with the given observed state.
func seedSourceVM(t *testing.T, migrationManager *MigrationManager, machines *Manager, state State, acknowledged int) {
	t.Helper()
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", state)
	machines.runtime.(*fakeRuntime).state = state
	record := SourceMigrationRecord{
		ID: "mig-1", VirtualMachineID: "vm-1", Caller: "metal-1",
		Sequence: acknowledged, AcknowledgedSequence: acknowledged,
	}
	if err := migrationManager.store.writeSource(record); err != nil {
		t.Fatal(err)
	}
}

func TestStopSourceStopsAndCreatesFinalSnapshot(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	transfer.guid = "final-guid"
	transfer.sizeBytes = 500
	seedSourceVM(t, migrationManager, machines, StateRunning, 2)
	runtime := machines.runtime.(*fakeRuntime)
	network := machines.network.(*fakeNetwork)

	snapshot, err := migrationManager.StopSource(context.Background(), "mig-1", "vm-1", "metal-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sequence != 3 || snapshot.SizeBytes != 500 || snapshot.GUID != "final-guid" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if runtime.stops != 1 || runtime.state != StateStopped {
		t.Fatalf("runtime stops = %d, state = %s", runtime.stops, runtime.state)
	}
	if network.releases != 1 {
		t.Fatalf("network releases = %d, want 1", network.releases)
	}
	// The source removes the stale candidate before the final snapshot.
	if want := []string{"migration-mig-1-3"}; !equalStringSlices(transfer.removed, want) {
		t.Fatalf("removed = %v", transfer.removed)
	}
	if want := []string{"migration-mig-1-3"}; !equalStringSlices(transfer.created, want) {
		t.Fatalf("created = %v", transfer.created)
	}
}

func TestStopSourceIsIdempotent(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	seedSourceVM(t, migrationManager, machines, StateRunning, 0)
	runtime := machines.runtime.(*fakeRuntime)
	network := machines.network.(*fakeNetwork)
	ctx := context.Background()

	first, err := migrationManager.StopSource(ctx, "mig-1", "vm-1", "metal-1")
	if err != nil {
		t.Fatal(err)
	}
	created := len(transfer.created)

	second, err := migrationManager.StopSource(ctx, "mig-1", "vm-1", "metal-1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != first.Sequence || first.Sequence != 1 {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	if runtime.stops != 1 || network.releases != 1 || len(transfer.created) != created {
		t.Fatalf("repeat did work again: stops %d, releases %d, created %d", runtime.stops, network.releases, len(transfer.created))
	}
}

func TestSendSourceStreamAppliesDiskLimit(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	seedSourceVM(t, migrationManager, machines, StateRunning, 1)
	runtime := machines.runtime.(*fakeRuntime)
	ctx := context.Background()

	var buffer bytes.Buffer
	if _, err := migrationManager.SendSourceStream(ctx, "mig-1", "vm-1", "metal-1", 1, "", 32, &buffer); err != nil {
		t.Fatal(err)
	}
	if runtime.diskLimitMiBps != 32 {
		t.Fatalf("applied disk limit = %d, want 32", runtime.diskLimitMiBps)
	}
	record, err := migrationManager.store.readSource("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.TemporaryDiskLimitMiBps != 32 {
		t.Fatalf("persisted disk limit = %d, want 32", record.TemporaryDiskLimitMiBps)
	}
}

func TestLimitSourceDiskNeverRaisesConfigured(t *testing.T) {
	machines, runtime, _, _ := newTestManager(t)
	ctx := context.Background()
	specification := testSpecification()
	specification.Disk.ThroughputMiBps = 16
	if _, err := machines.Create(ctx, "vm-1", specification); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateRunning)
	runtime.state = StateRunning

	applied, err := machines.LimitSourceDisk(ctx, "vm-1", 32)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 16 || runtime.diskLimitMiBps != 16 {
		t.Fatalf("applied = %d, runtime = %d, want the configured 16", applied, runtime.diskLimitMiBps)
	}
}

func TestUnlockSourceRemovesSnapshotsAndRestoresDiskLimit(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	transfer := migrationManager.transfer.(*fakeTransfer)
	seedSourceVM(t, migrationManager, machines, StateRunning, 2)
	record, _ := migrationManager.store.readSource("vm-1")
	record.TemporaryDiskLimitMiBps = 32
	if err := migrationManager.store.writeSource(record); err != nil {
		t.Fatal(err)
	}
	runtime := machines.runtime.(*fakeRuntime)

	if err := migrationManager.UnlockSource(context.Background(), "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"migration-mig-1-1", "migration-mig-1-2"}; !equalStringSlices(transfer.removed, want) {
		t.Fatalf("removed = %v", transfer.removed)
	}
	if runtime.diskRefresh != 1 {
		t.Fatalf("disk refresh = %d, want the configured limit restored", runtime.diskRefresh)
	}
	if _, err := migrationManager.store.readSource("vm-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source record still present: %v", err)
	}
}

func TestUnlockSourceRefusesAStoppedSourceBeforeRollback(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	seedSourceVM(t, migrationManager, machines, StateRunning, 1)
	record, _ := migrationManager.store.readSource("vm-1")
	record.Stopped = true
	if err := migrationManager.store.writeSource(record); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.UnlockSource(context.Background(), "mig-1", "vm-1", "metal-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unlock a stopped source before rollback = %v, want ErrConflict", err)
	}
}

func TestStartSourceRollbackRestoresNetworkAndState(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	seedSourceVM(t, migrationManager, machines, StateStopped, 1)
	record, _ := migrationManager.store.readSource("vm-1")
	record.Stopped = true
	record.OriginalDesired = StateRunning
	if err := migrationManager.store.writeSource(record); err != nil {
		t.Fatal(err)
	}
	runtime := machines.runtime.(*fakeRuntime)
	network := machines.network.(*fakeNetwork)

	if err := migrationManager.StartSourceRollback(context.Background(), "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatal(err)
	}
	if network.ensures != 1 || runtime.coldStarts != 1 {
		t.Fatalf("ensures = %d, cold starts = %d", network.ensures, runtime.coldStarts)
	}
	restored, _ := migrationManager.store.readSource("vm-1")
	if !restored.RollbackComplete {
		t.Fatal("rollback checkpoint was not written")
	}
}

func TestDestroySourceDestroysAndRemoves(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	seedSourceVM(t, migrationManager, machines, StateStopped, 1)
	record, _ := migrationManager.store.readSource("vm-1")
	record.Stopped = true
	record.NetworkRemoved = true
	record.FinalSequence = 1
	if err := migrationManager.store.writeSource(record); err != nil {
		t.Fatal(err)
	}
	runtime := machines.runtime.(*fakeRuntime)
	storage := machines.storage.(*fakeStorage)

	if err := migrationManager.DestroySource(context.Background(), "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.removes != 1 || storage.releases != 1 {
		t.Fatalf("removes = %d, releases = %d", runtime.removes, storage.releases)
	}
	if _, err := machines.store.readDesired("vm-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("VM records still present: %v", err)
	}
}

func TestDestroySourceRequiresAStoppedSource(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	seedSourceVM(t, migrationManager, machines, StateRunning, 1)
	if err := migrationManager.DestroySource(context.Background(), "mig-1", "vm-1", "metal-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("destroy a source that never stopped = %v, want ErrConflict", err)
	}
}

func TestDestroySourceIsIdempotentWhenGone(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	if err := migrationManager.DestroySource(context.Background(), "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatalf("destroy an already-gone source = %v, want nil", err)
	}
}

func TestDestroySourceRefusesARemnantWithoutASourceRecord(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	if _, err := machines.Create(context.Background(), "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateStopped)
	if err := migrationManager.DestroySource(context.Background(), "mig-1", "vm-1", "metal-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("destroy a VM remnant with no source record = %v, want ErrConflict", err)
	}
}

func equalStringSlices(got, want []string) bool {
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

func setObservedState(t *testing.T, machines *Manager, virtualMachineID string, state State) {
	t.Helper()
	record := ObservedRecord{State: state, Generation: 1, UpdatedAt: time.Now().UTC()}
	if err := machines.store.writeObserved(virtualMachineID, record); err != nil {
		t.Fatal(err)
	}
}

func TestLockSourceReturnsPortableConfigAndLocks(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateRunning)

	handshake, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-1")
	if err != nil {
		t.Fatal(err)
	}
	if handshake.ObservedState != StateRunning {
		t.Fatalf("observed = %s", handshake.ObservedState)
	}
	if handshake.Config.VirtualMachineID != "vm-1" || handshake.Config.CreateFingerprint == "" ||
		handshake.Config.Specification.MemoryMiB != 2048 {
		t.Fatalf("config = %+v", handshake.Config)
	}
	if !machines.isSourceLocked("vm-1") {
		t.Fatal("VM was not source locked")
	}
}

func TestLockSourceRejectsAMissingVirtualMachine(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	if _, err := migrationManager.LockSource(context.Background(), "mig-1", "vm-1", "metal-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing VM = %v, want ErrNotFound", err)
	}
}

func TestLockSourceRejectsFailedOrUnknownStates(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}

	// A fresh VM reports unknown state.
	if _, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown state = %v, want ErrConflict", err)
	}

	setObservedState(t, machines, "vm-1", StateFailed)
	if _, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("failed state = %v, want ErrConflict", err)
	}
}

func TestLockSourceIsIdempotentForOneMigration(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateRunning)
	if _, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatalf("repeat lock = %v, want nil", err)
	}
	if _, err := migrationManager.LockSource(ctx, "mig-2", "vm-1", "metal-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second migration = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-9"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong caller = %v, want ErrConflict", err)
	}
}

func TestUnlockSourceClearsTheLock(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateRunning)
	if _, err := migrationManager.LockSource(ctx, "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.UnlockSource(ctx, "mig-1", "vm-1", "metal-9"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong caller unlock = %v, want ErrConflict", err)
	}
	if err := migrationManager.UnlockSource(ctx, "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatal(err)
	}
	if machines.isSourceLocked("vm-1") {
		t.Fatal("source lock still present after unlock")
	}
	if err := migrationManager.UnlockSource(ctx, "mig-1", "vm-1", "metal-1"); err != nil {
		t.Fatalf("repeat unlock = %v, want nil", err)
	}
}
