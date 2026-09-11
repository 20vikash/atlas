package vm

import (
	"context"
	"errors"
	"testing"
)

// lockSource writes a source migration record, which is the source lock.
func lockSource(t *testing.T, manager *Manager, virtualMachineID string) {
	t.Helper()
	store := newMigrationStore(manager.configuration.MachinesDirectory)
	record := SourceMigrationRecord{ID: "mig-1", VirtualMachineID: virtualMachineID}
	if err := store.writeSource(record); err != nil {
		t.Fatal(err)
	}
}

func TestSourceLockBlocksMutationsButAllowsReads(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := manager.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	lockSource(t, manager, "vm-1")

	for name, mutate := range map[string]func() error{
		"power":   func() error { return manager.SetPowerState(ctx, "vm-1", StateStopped) },
		"restart": func() error { return manager.RequestRestart(ctx, "vm-1") },
		"network": func() error { return manager.SetNetwork(ctx, "vm-1", NetworkConfiguration{Egress: EgressMesh}) },
		"delete":  func() error { return manager.Delete(ctx, "vm-1") },
	} {
		if err := mutate(); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s mutation during migration = %v, want ErrConflict", name, err)
		}
	}

	if _, err := manager.Information(ctx, "vm-1"); err != nil {
		t.Fatalf("read during migration = %v, want nil", err)
	}
}

func TestSourceLockSkipsReconciliation(t *testing.T) {
	manager, _, network, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := manager.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	lockSource(t, manager, "vm-1")

	if err := manager.Reconcile(ctx, "vm-1"); err != nil {
		t.Fatalf("reconcile of a locked source = %v, want nil", err)
	}
	if network.ensures != 0 {
		t.Fatalf("reconcile touched the network %d times, want 0", network.ensures)
	}
}

func TestClearedSourceLockRestoresMutations(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := manager.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	lockSource(t, manager, "vm-1")
	if err := newMigrationStore(manager.configuration.MachinesDirectory).remove("vm-1"); err != nil {
		t.Fatal(err)
	}

	if err := manager.SetPowerState(ctx, "vm-1", StateStopped); err != nil {
		t.Fatalf("power change after unlock = %v, want nil", err)
	}
	if manager.isSourceLocked("vm-1") {
		t.Fatal("source lock still present after clear")
	}
}
