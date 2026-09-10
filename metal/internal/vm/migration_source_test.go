package vm

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// writeSourceRecord sets up a source lock record for the transfer tests.
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
	// Acknowledging sequence 2 removes sequence 1 and keeps 2 as the base.
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
	written, err := migrationManager.SendSourceStream(ctx, "mig-1", "vm-1", "metal-1", 2, "", &buffer)
	if err != nil || written != 2048 {
		t.Fatalf("send = %d, %v", written, err)
	}
	if want := []string{"migration-mig-1-2|migration-mig-1-1|"}; !equalStringSlices(transfer.sent, want) {
		t.Fatalf("sent = %v", transfer.sent)
	}
	if _, err := migrationManager.SendSourceStream(ctx, "mig-1", "vm-1", "metal-1", 3, "", &buffer); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown sequence = %v, want ErrConflict", err)
	}
}

// seedSourceVM creates a source VM, sets its observed state, and writes a source
// lock record with the given acknowledged sequence.
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
	// The source removes the stale candidate, then takes the final snapshot.
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

	// A fresh VM still reports the unknown state.
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
