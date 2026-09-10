package vm

import (
	"context"
	"errors"
	"testing"
)

type fakeSourceClient struct {
	prepareCalls  int
	removeCalls   int
	removeError   error
	prepareConfig PortableConfig
	prepareState  State
	prepareError  error
}

func (c *fakeSourceClient) PrepareSource(context.Context, string, string, string, string) (PortableConfig, State, error) {
	c.prepareCalls++
	return c.prepareConfig, c.prepareState, c.prepareError
}

func (c *fakeSourceClient) RemoveSource(context.Context, string, string, string) error {
	c.removeCalls++
	return c.removeError
}

func ampleCapacity(context.Context) (AvailableCapacity, error) {
	return AvailableCapacity{CPUCount: 64, MemoryMiB: 262144, StorageMiB: 4194304}, nil
}

func newMigrationManager(t *testing.T) (*MigrationManager, *Manager, *fakeSourceClient) {
	t.Helper()
	machines, _, _, _ := newTestManager(t)
	source := &fakeSourceClient{}
	migrationManager, err := NewMigrationManager(machines, source, ampleCapacity, nil)
	if err != nil {
		t.Fatal(err)
	}
	return migrationManager, machines, source
}

func TestCreateTargetReservesAndRefreshesTheToken(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()

	record, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationRunning || record.Phase != PhasePreparing {
		t.Fatalf("record = %+v", record)
	}
	if !machines.isTargetReserved("vm-1") {
		t.Fatal("VM ID was not reserved")
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-2"); err != nil {
		t.Fatalf("idempotent retry = %v", err)
	}
	token, err := newMigrationStore(machines.configuration.MachinesDirectory).readToken("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok-2" {
		t.Fatalf("token = %q, want the refreshed value", token)
	}
}

func TestCreateTargetRejectsChangedValues(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.9:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.CreateTarget(ctx, "mig-2", "vm-1", "http://10.0.0.3:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed migration ID = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-2", "http://10.0.0.3:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused migration ID for another VM = %v, want ErrConflict", err)
	}
}

func TestCreateTargetRejectsALiveVirtualMachineID(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reserve a live VM ID = %v, want ErrConflict", err)
	}
}

func TestCreateRejectsATargetReservedID(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-9", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := machines.Create(ctx, "vm-9", testSpecification()); !errors.Is(err, ErrConflict) {
		t.Fatalf("create a target-reserved ID = %v, want ErrConflict", err)
	}
}

func TestTargetStatusResolvesTheMigrationID(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	record, err := migrationManager.TargetStatus(ctx, "mig-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.VirtualMachineID != "vm-1" {
		t.Fatalf("status = %+v", record)
	}
	if _, err := migrationManager.TargetStatus(ctx, "mig-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing status = %v, want ErrNotFound", err)
	}
}

func TestAbortTargetUnlocksTheSourceAndClearsTheReservation(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AbortTarget(ctx, "mig-1"); err != nil {
		t.Fatal(err)
	}
	if source.removeCalls != 1 {
		t.Fatalf("RemoveSource calls = %d, want 1", source.removeCalls)
	}
	if machines.isTargetReserved("vm-1") {
		t.Fatal("reservation still present after abort")
	}
}

func TestAbortTargetKeepsRecordsWhenCleanupFails(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	source.removeError = errors.New("source unreachable")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AbortTarget(ctx, "mig-1"); err == nil {
		t.Fatal("want a cleanup error")
	}
	if !machines.isTargetReserved("vm-1") {
		t.Fatal("reservation was cleared despite a cleanup failure")
	}
}

func TestTargetReservationsCountOnlyMigrationsWithConfig(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	reservations, err := migrationManager.TargetReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 0 {
		t.Fatalf("preparing migration reserved capacity: %+v", reservations)
	}

	store := newMigrationStore(machines.configuration.MachinesDirectory)
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = PhaseCopying
	record.Config = &PortableConfig{Specification: Specification{VirtualCPUCount: 3, MemoryMiB: 3072, DiskMiB: 8192}}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	reservations, err = migrationManager.TargetReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 1 || reservations[0].VirtualCPUCount != 3 || reservations[0].MemoryMiB != 3072 {
		t.Fatalf("reservations = %+v", reservations)
	}
}
