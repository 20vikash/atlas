package vm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTargetRecord() TargetMigrationRecord {
	return TargetMigrationRecord{
		ID:               "mig-1",
		VirtualMachineID: "vm-1",
		Source:           "http://10.0.0.3:9000",
		Status:           MigrationRunning,
		Phase:            PhasePreparing,
		CreatedAt:        time.Now().UTC(),
	}
}

func TestTargetRecordRoundTrips(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	record := newTargetRecord()
	record.Config = &PortableConfig{VirtualMachineID: "vm-1", Specification: Specification{VirtualCPUCount: 2, MemoryMiB: 2048}}
	record.UserID = 100001
	record.GroupID = 100001

	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "mig-1" || got.Config == nil || got.Config.Specification.MemoryMiB != 2048 || got.UserID != 100001 {
		t.Fatalf("target record = %+v", got)
	}
}

func TestSourceRecordRoundTrips(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	record := SourceMigrationRecord{
		ID:               "mig-1",
		VirtualMachineID: "vm-1",
		Caller:           "metal-2",
		OriginalDesired:  StateRunning,
		OriginalObserved: StateRunning,
		LockedAt:         time.Now().UTC(),
	}
	if err := store.writeSource(record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readSource("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Caller != "metal-2" || got.OriginalDesired != StateRunning {
		t.Fatalf("source record = %+v", got)
	}
}

func TestTokenFileIsOwnerOnly(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	if err := store.writeToken("vm-1", "signed-token"); err != nil {
		t.Fatal(err)
	}
	got, err := store.readToken("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "signed-token" {
		t.Fatalf("token = %q", got)
	}
	info, err := os.Stat(store.tokenPath("vm-1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o", info.Mode().Perm())
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
	if _, _, err := store.findTarget("mig-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("find missing migration = %v, want ErrNotFound", err)
	}
}

func TestListVirtualMachineIDsFindsTargetAndSource(t *testing.T) {
	store := newMigrationStore(t.TempDir())
	if err := store.writeTarget(newTargetRecord()); err != nil {
		t.Fatal(err)
	}
	source := SourceMigrationRecord{ID: "mig-2", VirtualMachineID: "vm-2", Caller: "metal-1"}
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

func TestValidateAllRejectsACorruptRecord(t *testing.T) {
	directory := t.TempDir()
	migrationDirectory := filepath.Join(directory, "vm-1", migrationSubdirectory)
	if err := os.MkdirAll(migrationDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migrationDirectory, targetFileName), []byte(`{"schema_version":1,"id":"mig-1"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := newMigrationStore(directory).validateAll(); err == nil {
		t.Fatal("want an invalid target record error")
	}
}

func TestReadTargetRejectsAnUnknownField(t *testing.T) {
	directory := t.TempDir()
	migrationDirectory := filepath.Join(directory, "vm-1", migrationSubdirectory)
	if err := os.MkdirAll(migrationDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":1,"id":"mig-1","virtual_machine_id":"vm-1","source":"x","status":"running","phase":"preparing","created_at":"2026-01-01T00:00:00Z","extra":true}`
	if err := os.WriteFile(filepath.Join(migrationDirectory, targetFileName), []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := newMigrationStore(directory).readTarget("vm-1"); err == nil {
		t.Fatal("want an unknown field error")
	}
}

func TestRemoveKeepsTheVirtualMachineRecords(t *testing.T) {
	directory := t.TempDir()
	store := newMigrationStore(directory)
	virtualMachineRecord := filepath.Join(directory, "vm-1", desiredFileName)
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
