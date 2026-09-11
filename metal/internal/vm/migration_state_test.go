package vm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// setObservedState writes one VM's observed state for a migration test.
func setObservedState(t *testing.T, machines *Manager, virtualMachineID string, state State) {
	t.Helper()
	record := ObservedRecord{State: state, Generation: 1, UpdatedAt: time.Now().UTC()}
	if err := machines.store.writeObserved(virtualMachineID, record); err != nil {
		t.Fatal(err)
	}
}

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

// seedTargetVM writes reconstructed target records in the original state.
func seedTargetVM(t *testing.T, machines *Manager, desiredState State) {
	t.Helper()
	desired := DesiredRecord{
		ID: "vm-1", UserID: 1000, GroupID: 1000, State: desiredState,
		CreateFingerprint:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Specification:           testSpecification(),
		Generation:              2,
		SpecificationGeneration: 1,
		RestartGeneration:       1,
	}
	if err := machines.store.writeDesired(desired); err != nil {
		t.Fatal(err)
	}
	setObservedState(t, machines, "vm-1", StateUnknown)
}

func TestEnsureMigrationNetworkRecordsTheInterface(t *testing.T) {
	machines, _, network, _ := newTestManager(t)
	seedTargetVM(t, machines, StateRunning)

	if err := machines.EnsureMigrationNetwork(context.Background(), "vm-1"); err != nil {
		t.Fatal(err)
	}
	if network.ensures != 1 {
		t.Fatalf("network ensures = %d, want 1", network.ensures)
	}
	observed, err := machines.store.readObserved("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.NetworkInterface.MACAddress == "" {
		t.Fatal("network interface was not recorded")
	}
}

func TestApplyMigratedTargetState(t *testing.T) {
	cases := []struct {
		name           string
		desiredState   State
		wantColdStarts int
		wantPauses     int
	}{
		{"running cold-starts", StateRunning, 1, 0},
		{"paused cold-starts then pauses", StatePaused, 1, 1},
		{"stopped keeps the runtime stopped", StateStopped, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machines, runtime, _, _ := newTestManager(t)
			runtime.state = StateStopped
			seedTargetVM(t, machines, tc.desiredState)

			if err := machines.ApplyMigratedTargetState(context.Background(), "vm-1"); err != nil {
				t.Fatal(err)
			}
			if runtime.coldStarts != tc.wantColdStarts || runtime.pauses != tc.wantPauses {
				t.Fatalf("cold starts = %d, pauses = %d", runtime.coldStarts, runtime.pauses)
			}
			observed, err := machines.store.readObserved("vm-1")
			if err != nil {
				t.Fatal(err)
			}
			if observed.State != tc.desiredState || observed.Generation != 2 {
				t.Fatalf("observed = %s at generation %d", observed.State, observed.Generation)
			}
		})
	}
}

func TestApplyMigratedTargetStateReportsAStartFailure(t *testing.T) {
	machines, runtime, _, _ := newTestManager(t)
	runtime.state = StateStopped
	runtime.coldStartError = errors.New("boot failed")
	seedTargetVM(t, machines, StateRunning)

	if err := machines.ApplyMigratedTargetState(context.Background(), "vm-1"); err == nil {
		t.Fatal("want a cold-start failure")
	}
	observed, err := machines.store.readObserved("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State == StateRunning {
		t.Fatal("a failed start must not record running")
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
