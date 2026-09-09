package vm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeStatusFile(t *testing.T, directory, identifier, body string) string {
	t.Helper()
	machineDirectory := filepath.Join(directory, identifier)
	if err := os.MkdirAll(machineDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(machineDirectory, "status.json")
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestObservedRecordRoundTripsSpecificationGeneration(t *testing.T) {
	store := newRecordStore(t.TempDir())
	record := ObservedRecord{
		Generation: 2, SpecificationGeneration: 3, RestartGeneration: 4,
		State: StateStopped, UpdatedAt: time.Now().UTC(),
	}
	if err := store.writeObserved("vm-1", record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readObserved("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SpecificationGeneration != 3 || got.State != StateStopped {
		t.Fatalf("observed record = %+v", got)
	}
}

func TestObservedRecordRejectsInternalSleepData(t *testing.T) {
	directory := t.TempDir()
	path := writeStatusFile(t, directory, "vm-1", `{"schema_version":1,"state":"stopped","updated_at":"2026-01-01T00:00:00Z","sleep":{}}`)
	if _, err := newRecordStore(directory).readObserved("vm-1"); err == nil {
		t.Fatal("want an unknown field error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
