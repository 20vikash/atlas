package migration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

func virtualMachineDefinition(virtualMachineID string) VirtualMachineDefinition {
	return VirtualMachineDefinition{
		VirtualMachineID:        virtualMachineID,
		CreateFingerprint:       strings.Repeat("a", 64),
		Generation:              1,
		SpecificationGeneration: 1,
		DesiredState:            vm.StateRunning,
		Specification:           testSpecification(),
	}
}

func TestAdvanceTargetPreparesTheSource(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	source.prepareDefinition = virtualMachineDefinition("vm-1")
	source.prepareState = vm.StateRunning
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	record, err := migrationManager.store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Definition == nil {
		t.Fatalf("record = %+v", record)
	}

	desired, err := machines.ReadDesired("vm-1")
	if err != nil {
		t.Fatalf("placeholder record missing: %v", err)
	}
	if desired.Specification.MemoryMiB != 2048 || desired.UserID == 0 {
		t.Fatalf("placeholder = %+v", desired)
	}
}

func TestAdvanceTargetIsANoOpWhileCopying(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	source.prepareDefinition = virtualMachineDefinition("vm-1")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
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
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}
	store := newMigrationStore(machines.VirtualMachineRecordsDirectory())
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
	awaitTransfer(t, migrationManager, "vm-1")
	if source.removeCalls != 1 {
		t.Fatalf("RemoveSource calls = %d, want 1", source.removeCalls)
	}
	if migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("reservation kept after expiry")
	}
}

func TestAdvanceTargetRecordsSourcePreparationErrors(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	source.prepareError = errors.New("source unreachable")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err == nil {
		t.Fatal("want a description error")
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
	source.prepareDefinition = virtualMachineDefinition("vm-other")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err == nil {
		t.Fatal("want a definition mismatch error")
	}
}

func TestAdvanceTargetRejectsInsufficientCapacity(t *testing.T) {
	machines := newFakeMigrationHost(t)
	source := &fakeSourceOperations{prepareDefinition: virtualMachineDefinition("vm-1"), prepareState: vm.StateRunning}
	noCapacity := func(context.Context) (AvailableCapacity, error) { return AvailableCapacity{}, nil }
	migrationManager, err := newManager(machines, source, &fakeMigrationStorage{}, noCapacity, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("advance without capacity = %v, want ErrConflict", err)
	}
}

func TestTargetCapacityCheckAndReservationAreSerialized(t *testing.T) {
	machines := newFakeMigrationHost(t)
	var migrationManager *Manager
	capacity := func(ctx context.Context) (AvailableCapacity, error) {
		reservations, err := migrationManager.TargetReservations(ctx)
		if err != nil {
			return AvailableCapacity{}, err
		}
		availableMemoryMiB := 3072
		for _, reservation := range reservations {
			availableMemoryMiB -= reservation.MemoryMiB
		}
		return AvailableCapacity{MemoryMiB: availableMemoryMiB, StorageMiB: 1 << 20}, nil
	}
	var err error
	migrationManager, err = newManager(machines, &fakeSourceOperations{}, &fakeMigrationStorage{}, capacity, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	records := make([]targetRecord, 2)
	for index, virtualMachineID := range []string{"vm-1", "vm-2"} {
		_, err := migrationManager.CreateTarget(ctx, "mig-"+virtualMachineID, virtualMachineID, "https://10.0.0.3:9000")
		if err != nil {
			t.Fatal(err)
		}
		record, err := migrationManager.store.readTarget(virtualMachineID)
		if err != nil {
			t.Fatal(err)
		}
		records[index] = record
	}

	results := make(chan error, len(records))
	var waitGroup sync.WaitGroup
	for _, record := range records {
		waitGroup.Add(1)
		go func(record targetRecord) {
			defer waitGroup.Done()
			definition := virtualMachineDefinition(record.VirtualMachineID)
			results <- migrationManager.reserveAndReconstructTarget(ctx, record, definition, vm.StateRunning)
		}(record)
	}
	waitGroup.Wait()
	close(results)

	succeeded, rejected := 0, 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, vm.ErrConflict):
			rejected++
		default:
			t.Fatalf("reservation result = %v", result)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("succeeded = %d, rejected = %d", succeeded, rejected)
	}
}

func TestActiveTargetVirtualMachineIDsListsRunningTargets(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
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

// writeReadyTarget writes a ready target record for finish tests.
func writeReadyTarget(t *testing.T, machines *fakeMigrationHost, mutate func(*targetRecord)) *migrationStore {
	t.Helper()
	store := newMigrationStore(machines.VirtualMachineRecordsDirectory())
	record := targetRecord{
		ID:               "mig-1",
		VirtualMachineID: "vm-1",
		Source:           "https://10.0.0.3:9000",
		State:            targetReady,
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
	transfer := migrationManager.disks.(*fakeMigrationStorage)
	store := writeReadyTarget(t, machines, func(r *targetRecord) {
		r.State = targetFinishing
		r.Intervals = []TransferProgress{
			{Sequence: 1, Completed: true},
			{Sequence: 2, Completed: true},
		}
	})

	migrationManager.runTransfer(context.Background(), "vm-1")

	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != targetCompleted || record.FinishedAt.IsZero() {
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
	if record.State != targetFinishing {
		t.Fatal("finish request was not recorded")
	}
}

func TestRequestFinishRequiresReady(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeCopyingTarget(t, machines, vm.StateRunning)

	if err := migrationManager.RequestFinish(context.Background(), "mig-1"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("finish before ready = %v, want ErrConflict", err)
	}
}

func TestRequestFinishConflictsWithAbort(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeReadyTarget(t, machines, func(r *targetRecord) { r.State = targetRemovingRuntime })

	if err := migrationManager.RequestFinish(context.Background(), "mig-1"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("finish after abort = %v, want ErrConflict", err)
	}
}

func TestRunTransferAbortsBeforeStop(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	transfer := migrationManager.disks.(*fakeMigrationStorage)
	store := writeCopyingTarget(t, machines, vm.StateRunning)
	record, _ := store.readTarget("vm-1")
	record.State = targetRemovingRuntime
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	rec, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.status() != StatusAborted || rec.State.phase() != "" {
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
	store := writeCopyingTarget(t, machines, vm.StateRunning)
	record, _ := store.readTarget("vm-1")
	record.SourceStopped = true
	record.State = targetRemovingRuntime
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	rec, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.status() != StatusAborted {
		t.Fatalf("status = %s, want aborted", rec.State.status())
	}
	// A stopped source is restored, then unlocked.
	if source.startCalls != 1 || source.removeCalls != 1 {
		t.Fatalf("start calls = %d, remove calls = %d", source.startCalls, source.removeCalls)
	}
}

func TestRunTransferKeepsLockedWhenRollbackFails(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	source.removeError = errors.New("source unreachable")
	store := writeCopyingTarget(t, machines, vm.StateRunning)
	record, _ := store.readTarget("vm-1")
	record.State = targetRemovingRuntime
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	migrationManager.runTransfer(context.Background(), "vm-1")

	rec, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State.terminal() {
		t.Fatal("a failed rollback must keep the migration locked")
	}
	if rec.State != targetUnlockingSource {
		t.Fatal("the abort request must be preserved for a retry")
	}
}

func TestAdvanceTargetRollsBackAnAbandonedTarget(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}
	store := newMigrationStore(machines.VirtualMachineRecordsDirectory())
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	// Atlas stopped calling, so the target must release its reservation.
	record.State = targetCopying
	record.LastControlAt = time.Now().Add(-targetIdleTimeout - time.Minute).UTC()
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	awaitTransfer(t, migrationManager, "vm-1")

	if source.removeCalls != 1 {
		t.Fatalf("RemoveSource calls = %d, want 1", source.removeCalls)
	}
	if migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("an abandoned target must release its reservation")
	}
}

func TestAdvanceTargetKeepsAReadyTarget(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "https://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}
	store := newMigrationStore(machines.VirtualMachineRecordsDirectory())
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	// A ready target may already own the VM.
	record.State = targetReady
	record.LastControlAt = time.Now().Add(-targetIdleTimeout - time.Hour).UTC()
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}

	if !migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("a ready target must keep its reservation")
	}
}

// writeTerminalTarget writes a terminal target record.
func writeTerminalTarget(t *testing.T, machines *fakeMigrationHost, state targetState) *migrationStore {
	t.Helper()
	store := newMigrationStore(machines.VirtualMachineRecordsDirectory())
	record := targetRecord{
		ID: "mig-1", VirtualMachineID: "vm-1", State: state,
		CreatedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTerminalTargetDoesNotHideOrReserve(t *testing.T) {
	for _, state := range []targetState{targetCompleted, targetAborted} {
		migrationManager, machines, _ := newMigrationManager(t)
		writeTerminalTarget(t, machines, state)
		if migrationManager.IsTargetReserved("vm-1") {
			t.Fatalf("%s record still reserves the VM", state)
		}
		reservations, err := migrationManager.TargetReservations(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(reservations) != 0 {
			t.Fatalf("%s record still reserves capacity: %+v", state, reservations)
		}
	}
}

func TestCreateTargetReplacesACleanAbortedRemnant(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeTerminalTarget(t, machines, targetAborted)

	record, err := migrationManager.CreateTarget(context.Background(), "mig-2", "vm-1", "https://10.0.0.9:9000")
	if err != nil {
		t.Fatalf("replace a clean aborted remnant = %v", err)
	}
	if record.ID != "mig-2" || record.Status != StatusRunning || record.Phase != PhasePreparing {
		t.Fatalf("record = %+v", record)
	}
}

func TestCreateTargetRefusesReplacingACompletedRecord(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	writeTerminalTarget(t, machines, targetCompleted)

	if _, err := migrationManager.CreateTarget(context.Background(), "mig-2", "vm-1", "https://10.0.0.9:9000"); err == nil {
		t.Fatal("a completed migration must not be replaced")
	}
}

func TestActiveTargetsIncludeFinishAndAbortWork(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	store := newMigrationStore(machines.VirtualMachineRecordsDirectory())
	// Ready finish and failed abort requests are both requeued.
	if err := store.writeTarget(targetRecord{
		ID: "mig-1", VirtualMachineID: "vm-1", State: targetFinishing, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.writeTarget(targetRecord{
		ID: "mig-2", VirtualMachineID: "vm-2", State: targetRemovingRuntime, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.writeTarget(terminalTargetRecord(targetRecord{ID: "mig-3", VirtualMachineID: "vm-3"}, targetCompleted, time.Now().UTC())); err != nil {
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
