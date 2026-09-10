package vm

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
