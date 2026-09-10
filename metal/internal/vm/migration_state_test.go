package vm

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeSourceToStopped(t *testing.T) {
	cases := []struct {
		name         string
		state        State
		savedState   bool
		wantStops    int
		wantResumes  int
		wantRestores int
	}{
		{"running stops", StateRunning, false, 1, 0, 0},
		{"created stops", StateCreated, false, 1, 0, 0},
		{"paused resumes then stops", StatePaused, false, 1, 1, 0},
		{"saved state restores then stops", StateStopped, true, 1, 0, 1},
		{"stopped without saved state is a no-op", StateStopped, false, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machines, runtime, _, _ := newTestManager(t)
			ctx := context.Background()
			if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
				t.Fatal(err)
			}
			setObservedState(t, machines, "vm-1", tc.state)
			runtime.state = tc.state
			runtime.hasSavedState = tc.savedState

			if err := machines.NormalizeSourceToStopped(ctx, "vm-1"); err != nil {
				t.Fatal(err)
			}
			if runtime.stops != tc.wantStops || runtime.resumes != tc.wantResumes || runtime.restores != tc.wantRestores {
				t.Fatalf("stops %d, resumes %d, restores %d", runtime.stops, runtime.resumes, runtime.restores)
			}
			if runtime.state != StateStopped {
				t.Fatalf("state = %s, want stopped", runtime.state)
			}
		})
	}
}

func TestNormalizeSourceToStoppedRefusesFailedState(t *testing.T) {
	machines, runtime, _, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateFailed)
	runtime.state = StateFailed

	if err := machines.NormalizeSourceToStopped(ctx, "vm-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("failed source = %v, want ErrConflict", err)
	}
}

func TestRemoveMigrationNetworkReleases(t *testing.T) {
	machines, _, network, _ := newTestManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}

	if err := machines.RemoveMigrationNetwork(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if network.releases != 1 {
		t.Fatalf("network releases = %d, want 1", network.releases)
	}
}
