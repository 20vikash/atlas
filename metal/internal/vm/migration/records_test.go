package migration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

func newTargetRecord() targetRecord {
	return targetRecord{
		ID:               "mig-1",
		VirtualMachineID: "vm-1",
		Source:           "https://10.0.0.3:9000",
		State:            targetPreparing,
		CreatedAt:        time.Now().UTC(),
	}
}

func TestTargetRecordRoundTrips(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	record := newTargetRecord()
	record.Definition = &VirtualMachineDefinition{VirtualMachineID: "vm-1", Specification: vm.Specification{CPUMillicores: 2000, MemoryMiB: 2048}}

	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "mig-1" || got.Definition == nil || got.Definition.Specification.MemoryMiB != 2048 {
		t.Fatalf("target record = %+v", got)
	}
}

func TestTerminalRecordNeedsNoPhase(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	record := newTargetRecord()
	record.State = targetCompleted
	record.FinishedAt = time.Now().UTC()
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatalf("a completed record with no phase must read back: %v", err)
	}
	if got.State != targetCompleted {
		t.Fatalf("record = %+v", got)
	}
}

func TestTargetRecordRequiresAState(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	record := newTargetRecord()
	record.State = ""
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.readTarget("vm-1"); err == nil {
		t.Fatal("a target record with no state must be rejected")
	}
}

func TestTransferStateRoundTrips(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	startedAt := time.Now().UTC().Add(-42 * time.Second)
	finishedAt := time.Now().UTC()
	target := newTargetRecord()
	target.State = targetCopying
	target.SourceObservedState = vm.StateRunning
	target.CopyStartedAt = time.Now().UTC()
	target.Intervals = []TransferProgress{
		{Sequence: 1, StartedAt: startedAt, FinishedAt: finishedAt, DurationSeconds: 42, BytesTransferred: 1024, TotalBytes: 1024, GUID: "g1", Completed: true},
		{Sequence: 2, BytesTransferred: 256, TotalBytes: 1024},
	}
	if err := store.writeTarget(target); err != nil {
		t.Fatal(err)
	}
	source := sourceRecord{
		ID: "mig-1", VirtualMachineID: "vm-2",
		State: sourceLocked, Sequence: 2, AcknowledgedSequence: 1,
	}
	if err := store.writeSource(source); err != nil {
		t.Fatal(err)
	}

	gotTarget, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTarget.Intervals) != 2 || !gotTarget.Intervals[0].Completed || !gotTarget.Intervals[0].FinishedAt.Equal(finishedAt) {
		t.Fatalf("target = %+v", gotTarget)
	}
	gotSource, err := store.readSource("vm-2")
	if err != nil {
		t.Fatal(err)
	}
	if gotSource.Sequence != 2 || gotSource.AcknowledgedSequence != 1 {
		t.Fatalf("source = %+v", gotSource)
	}
}

func TestSourceRecordRoundTrips(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	record := sourceRecord{
		ID:               "mig-1",
		VirtualMachineID: "vm-1",
		OriginalDesired:  vm.StateRunning,
		State:            sourceLocked,
		LastContactAt:    time.Now().UTC(),
	}
	if err := store.writeSource(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readSource("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginalDesired != vm.StateRunning {
		t.Fatalf("source record = %+v", got)
	}
}

func TestFindTargetResolvesTheMigrationID(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	if err := store.writeTarget(newTargetRecord()); err != nil {
		t.Fatal(err)
	}
	virtualMachineID, record, err := store.findTarget("mig-1")
	if err != nil {
		t.Fatal(err)
	}
	if virtualMachineID != "vm-1" || record.ID != "mig-1" {
		t.Fatalf("found %s -> %+v", virtualMachineID, record)
	}
	if _, _, err := store.findTarget("mig-missing"); !errors.Is(err, vm.ErrNotFound) {
		t.Fatalf("find missing migration = %v, want ErrNotFound", err)
	}
}

func TestListVirtualMachineIDsFindsTargetAndSource(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	if err := store.writeTarget(newTargetRecord()); err != nil {
		t.Fatal(err)
	}
	source := sourceRecord{ID: "mig-2", VirtualMachineID: "vm-2", State: sourceLocked}
	if err := store.writeSource(source); err != nil {
		t.Fatal(err)
	}
	ids, err := store.listVirtualMachineIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "vm-1" || ids[1] != "vm-2" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestValidateRejectsACorruptRecord(t *testing.T) {
	directory := t.TempDir()
	migrationDirectory := filepath.Join(directory, "vm-1", migrationSubdirectory)
	if err := os.MkdirAll(migrationDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	// A record missing required fields cannot drive a migration.
	if err := os.WriteFile(filepath.Join(migrationDirectory, targetFileName), []byte(`{"schema_version":2,"id":"mig-1"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	store := newMigrationStore(directory)

	if err := store.validate(); err == nil {
		t.Fatal("startup accepted a corrupt migration record")
	}
	if !store.has(store.targetPath("vm-1")) {
		t.Fatal("startup removed the corrupt migration record")
	}
}

func TestReadTargetRejectsAnUnknownField(t *testing.T) {
	directory := t.TempDir()
	migrationDirectory := filepath.Join(directory, "vm-1", migrationSubdirectory)
	if err := os.MkdirAll(migrationDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":2,"id":"mig-1","virtual_machine_id":"vm-1","source":"x","state":"preparing","created_at":"2026-01-01T00:00:00Z","caller":"gone"}`
	if err := os.WriteFile(filepath.Join(migrationDirectory, targetFileName), []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := newMigrationStore(directory).readTarget("vm-1"); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func TestRemoveKeepsTheVirtualMachineRecords(t *testing.T) {
	directory := t.TempDir()
	store := newMigrationStore(directory)
	virtualMachineRecord := filepath.Join(directory, "vm-1", "config.json")
	if err := os.MkdirAll(filepath.Dir(virtualMachineRecord), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(virtualMachineRecord, []byte(`{}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.writeTarget(newTargetRecord()); err != nil {
		t.Fatal(err)
	}

	if err := store.remove("vm-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(virtualMachineRecord); err != nil {
		t.Fatalf("VM record removed with the migration: %v", err)
	}
	ids, err := store.listVirtualMachineIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}
