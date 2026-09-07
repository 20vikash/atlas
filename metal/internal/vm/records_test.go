package vm

import (
	"testing"
	"time"
)

func newTestObserved(state State) ObservedRecord {
	return ObservedRecord{State: state, Generation: 1, UpdatedAt: time.Now().UTC()}
}

func TestObservedSleepProgressRoundTrips(t *testing.T) {
	store := newRecordStore(t.TempDir())
	record := newTestObserved(StateSleeping)
	record.Sleep = &SleepProgress{
		EligibleAt:            time.Unix(100, 0).UTC(),
		RequestedAt:           time.Unix(200, 0).UTC(),
		SnapshotGeneration:    3,
		SnapshotCreatedAt:     time.Unix(300, 0).UTC(),
		LastNetworkActivityAt: time.Unix(150, 0).UTC(),
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
	if got.Sleep.SnapshotGeneration != 3 {
		t.Errorf("snapshot generation = %d, want 3", got.Sleep.SnapshotGeneration)
	}
	if !got.Sleep.SnapshotCreatedAt.Equal(record.Sleep.SnapshotCreatedAt) ||
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
			record.Sleep = &SleepProgress{SnapshotGeneration: 1}
		},
		"creation time without a generation": func(record *ObservedRecord) {
			record.Sleep = &SleepProgress{SnapshotCreatedAt: time.Unix(1, 0).UTC()}
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
