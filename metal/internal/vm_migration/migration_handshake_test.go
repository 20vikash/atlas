package vmmigration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

func portableConfig(virtualMachineID string) PortableConfig {
	return PortableConfig{
		VirtualMachineID:        virtualMachineID,
		CreateFingerprint:       strings.Repeat("a", 64),
		Generation:              1,
		SpecificationGeneration: 1,
		DesiredState:            vm.StateRunning,
		Specification:           testSpecification(),
	}
}

func TestAdvanceTargetRunsTheHandshake(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	source.prepareConfig = portableConfig("vm-1")
	source.prepareState = vm.StateRunning
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	record, err := migrationManager.TargetStatus(ctx, "mig-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Config == nil || record.UserID == 0 {
		t.Fatalf("record = %+v", record)
	}

	desired, err := machines.ReadDesired("vm-1")
	if err != nil {
		t.Fatalf("placeholder record missing: %v", err)
	}
	if desired.Specification.MemoryMiB != 2048 || desired.UserID != record.UserID {
		t.Fatalf("placeholder = %+v", desired)
	}
}

func TestAdvanceTargetIsANoOpWhileCopying(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	source.prepareConfig = portableConfig("vm-1")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if source.prepareCalls != 1 {
		t.Fatalf("PrepareSource calls = %d, want 1", source.prepareCalls)
	}
}

func TestAdvanceTargetExpiresAStaleReservation(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}
	store := newMigrationStore(machines.MachinesDirectory())
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	record.CreatedAt = time.Now().Add(-11 * time.Minute).UTC()
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	// Expiry requests an abort and the worker rolls the reservation back.
	if err := migrationManager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if source.removeCalls != 1 {
		t.Fatalf("RemoveSource calls = %d, want 1", source.removeCalls)
	}
	if migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("reservation kept after expiry")
	}
}

func TestAdvanceTargetRecordsHandshakeErrors(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	source.prepareError = errors.New("source unreachable")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err == nil {
		t.Fatal("want a handshake error")
	}
	record, err := migrationManager.TargetStatus(ctx, "mig-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Error == nil || record.Phase != PhasePreparing {
		t.Fatalf("record = %+v", record)
	}
}

func TestAdvanceTargetRejectsAWrongConfig(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	source.prepareConfig = portableConfig("vm-other")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err == nil {
		t.Fatal("want a config mismatch error")
	}
}

func TestAdvanceTargetRejectsInsufficientCapacity(t *testing.T) {
	machines := newFakeMachines(t)
	source := &fakeSourceClient{prepareConfig: portableConfig("vm-1"), prepareState: vm.StateRunning}
	noCapacity := func(context.Context) (AvailableCapacity, error) { return AvailableCapacity{}, nil }
	migrationManager, err := NewVMMigration(machines, source, &fakeTransfer{}, noCapacity, MigrationSettings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("advance without capacity = %v, want ErrConflict", err)
	}
}

func TestActiveTargetVirtualMachineIDsListsRunningTargets(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	ids, err := migrationManager.ActiveTargetVirtualMachineIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "vm-1" {
		t.Fatalf("active targets = %v", ids)
	}
}
