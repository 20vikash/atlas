package vm

import (
	"context"
	"testing"
	"time"
)

// writeTerminalTarget writes a compact terminal target record for one VM.
func writeTerminalTarget(t *testing.T, machines *Manager, status MigrationStatus) *migrationStore {
	t.Helper()
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	record := TargetMigrationRecord{
		ID: "mig-1", VirtualMachineID: "vm-1", Status: status,
		CreatedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTerminalTargetDoesNotHideOrReserve(t *testing.T) {
	for _, status := range []MigrationStatus{MigrationCompleted, MigrationAborted} {
		migrationManager, machines, _ := newMigrationManager(t)
		writeTerminalTarget(t, machines, status)
		if machines.isTargetReserved("vm-1") {
			t.Fatalf("%s record still reserves the VM", status)
		}
		reservations, err := migrationManager.TargetReservations(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(reservations) != 0 {
			t.Fatalf("%s record still reserves capacity: %+v", status, reservations)
		}
	}
}

func TestCreateTargetReplacesACleanAbortedRemnant(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeTerminalTarget(t, machines, MigrationAborted)

	record, err := migrationManager.CreateTarget(context.Background(), "mig-2", "vm-1", "http://10.0.0.9:9000", "tok-2")
	if err != nil {
		t.Fatalf("replace a clean aborted remnant = %v", err)
	}
	if record.ID != "mig-2" || record.Status != MigrationRunning || record.Phase != PhasePreparing {
		t.Fatalf("record = %+v", record)
	}
}

func TestCreateTargetRefusesReplacingACompletedRecord(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeTerminalTarget(t, machines, MigrationCompleted)

	if _, err := migrationManager.CreateTarget(context.Background(), "mig-2", "vm-1", "http://10.0.0.9:9000", "tok-2"); err == nil {
		t.Fatal("a completed migration must not be replaced")
	}
}

func TestActiveTargetsIncludeFinishAndAbortWork(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	store := newMigrationStore(machines.configuration.MachinesDirectory)
	// A ready migration awaiting finish and a failed migration awaiting abort are
	// both pending work that the reconciler must requeue.
	if err := store.writeTarget(TargetMigrationRecord{
		ID: "mig-1", VirtualMachineID: "vm-1", Status: MigrationReady, Phase: PhaseStarting,
		FinishRequested: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.writeTarget(TargetMigrationRecord{
		ID: "mig-2", VirtualMachineID: "vm-2", Status: MigrationFailed, Phase: PhaseRollback,
		AbortRequested: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.writeTarget(terminalTargetRecord(TargetMigrationRecord{ID: "mig-3", VirtualMachineID: "vm-3"}, MigrationCompleted, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	active, err := migrationManager.ActiveTargetVirtualMachineIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("active = %v, want the two nonterminal migrations", active)
	}
}
