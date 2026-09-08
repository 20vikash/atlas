package vm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeStatusFile writes a hand-crafted status record.
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

func TestReadObservedLoadsAnOldRecordWithoutASleepObject(t *testing.T) {
	directory := t.TempDir()
	store := newRecordStore(directory)
	// A schema version 1 record written before the sleep feature has no sleep key.
	writeStatusFile(t, directory, "vm-1", `{"schema_version":1,"generation":1,"state":"running","updated_at":"2026-01-01T00:00:00Z"}`)

	got, err := store.readObserved("vm-1")
	if err != nil {
		t.Fatalf("old record did not load: %v", err)
	}
	if got.Sleep != nil {
		t.Error("an old record must read back with no sleep progress")
	}
	if got.State != StateRunning {
		t.Errorf("state = %s, want running", got.State)
	}
}

func TestReadObservedPreservesACorruptRecord(t *testing.T) {
	directory := t.TempDir()
	store := newRecordStore(directory)
	// sleeping with no sleep object is corrupt and must not be repaired.
	corrupt := `{"schema_version":1,"generation":1,"state":"sleeping","updated_at":"2026-01-01T00:00:00Z"}`
	path := writeStatusFile(t, directory, "vm-1", corrupt)

	if _, err := store.readObserved("vm-1"); err == nil {
		t.Fatal("want a validation error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != corrupt {
		t.Error("the corrupt record was changed; it must be kept for diagnosis")
	}
}

func newTestObserved(state State) ObservedRecord {
	return ObservedRecord{State: state, Generation: 1, UpdatedAt: time.Now().UTC()}
}

func TestObservedSleepProgressRoundTrips(t *testing.T) {
	store := newRecordStore(t.TempDir())
	record := newTestObserved(StateSleeping)
	record.Sleep = &SleepProgress{
		EligibleAt:               time.Unix(100, 0).UTC(),
		RequestedAt:              time.Unix(200, 0).UTC(),
		MemorySnapshotGeneration: 3,
		MemorySnapshotCreatedAt:  time.Unix(300, 0).UTC(),
		LastNetworkActivityAt:    time.Unix(150, 0).UTC(),
	}

	if err := store.writeObserved("vm-1", record); err != nil {
		t.Fatal(err)
	}
	got, err := store.readObserved("vm-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.Sleep == nil {
		t.Fatal("sleep progress was not read back")
	}
	if got.Sleep.MemorySnapshotGeneration != 3 {
		t.Errorf("snapshot generation = %d, want 3", got.Sleep.MemorySnapshotGeneration)
	}
	if !got.Sleep.MemorySnapshotCreatedAt.Equal(record.Sleep.MemorySnapshotCreatedAt) ||
		!got.Sleep.LastNetworkActivityAt.Equal(record.Sleep.LastNetworkActivityAt) ||
		!got.Sleep.RequestedAt.Equal(record.Sleep.RequestedAt) ||
		!got.Sleep.EligibleAt.Equal(record.Sleep.EligibleAt) {
		t.Errorf("sleep times did not round-trip: %+v", got.Sleep)
	}
}

func TestReadObservedRejectsCorruptSleepProgress(t *testing.T) {
	cases := map[string]func(*ObservedRecord){
		"sleeping without a sleep object": func(record *ObservedRecord) {
			record.State = StateSleeping
			record.Sleep = nil
		},
		"generation without a creation time": func(record *ObservedRecord) {
			record.Sleep = &SleepProgress{MemorySnapshotGeneration: 1}
		},
		"creation time without a generation": func(record *ObservedRecord) {
			record.Sleep = &SleepProgress{MemorySnapshotCreatedAt: time.Unix(1, 0).UTC()}
		},
		"sleeping without a published snapshot": func(record *ObservedRecord) {
			record.State = StateSleeping
			record.Sleep = &SleepProgress{RequestedAt: time.Unix(1, 0).UTC()}
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			store := newRecordStore(t.TempDir())
			record := newTestObserved(StateRunning)
			corrupt(&record)
			if err := store.writeObserved("vm-1", record); err != nil {
				t.Fatal(err)
			}
			if _, err := store.readObserved("vm-1"); err == nil {
				t.Fatal("want a validation error for corrupt sleep progress")
			}
		})
	}
}
