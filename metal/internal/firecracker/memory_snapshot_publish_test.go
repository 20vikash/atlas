package firecracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// writePendingMemorySnapshot writes a pending snapshot fixture.
func writePendingMemorySnapshot(t *testing.T, configuration Config, id string, stateSize, memorySize int) {
	t.Helper()
	pending := configuration.pendingMemorySnapshotDirectory(id)
	if err := os.MkdirAll(pending, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pending, memorySnapshotStateFileName), stateSize)
	writeFile(t, filepath.Join(pending, memorySnapshotMemoryFileName), memorySize)
}

func TestPublishPendingMemorySnapshotMovesAndValidates(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	runtime := &Runtime{configuration: configuration}
	machine := vm.RuntimeMachine{ID: "vm-1", UserID: 100001, SpecificationGeneration: 4, RestartGeneration: 1}
	writePendingMemorySnapshot(t, configuration, "vm-1", 2048, 4096)

	published, err := runtime.publishPendingMemorySnapshot(machine, 1)
	if err != nil {
		t.Fatal(err)
	}

	if published.Generation != 1 {
		t.Errorf("generation = %d, want 1", published.Generation)
	}
	if published.Manifest.SpecificationGeneration != 4 || published.Manifest.RestartGeneration != 1 {
		t.Errorf("manifest generations = %+v", published.Manifest)
	}
	if published.Manifest.StateFileSizeBytes != 2048 || published.Manifest.MemoryFileSizeBytes != 4096 {
		t.Errorf("manifest sizes = %+v", published.Manifest)
	}

	// The pending directory is consumed by the move.
	if _, err := os.Stat(configuration.pendingMemorySnapshotDirectory("vm-1")); !os.IsNotExist(err) {
		t.Error("pending directory was not moved")
	}
	// The published generation holds all three files.
	generationDirectory := configuration.memorySnapshotGenerationDirectory("vm-1", 1)
	for _, name := range []string{memorySnapshotStateFileName, memorySnapshotMemoryFileName, memorySnapshotManifestFileName} {
		if _, err := os.Stat(filepath.Join(generationDirectory, name)); err != nil {
			t.Errorf("published generation is missing %s: %v", name, err)
		}
	}
}

func TestPublishPendingMemorySnapshotRejectsAnIncompletePending(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	runtime := &Runtime{configuration: configuration}
	machine := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}
	// Create the pending directory with no artifact files.
	if err := os.MkdirAll(configuration.pendingMemorySnapshotDirectory("vm-1"), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.publishPendingMemorySnapshot(machine, 1); err == nil {
		t.Fatal("want an error for a missing artifact")
	}
}

// TestWarmStopRecognizesACompletedStop checks recovery of a published snapshot.
func TestWarmStopRecognizesACompletedStop(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	units := &stubUnits{active: false} // inactive reports stopped
	runtime := &Runtime{configuration: configuration, units: units, serialBroker: &stubSerialBroker{}}
	input := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}

	writePendingMemorySnapshot(t, configuration, "vm-1", 2048, 4096)
	if _, err := runtime.publishPendingMemorySnapshot(input, 1); err != nil {
		t.Fatal(err)
	}

	m := &machine{runtime: runtime, input: input, api: api.New(fcSocket(t, nil)), stopTimeout: time.Minute}
	if _, err := m.warmStop(context.Background()); err != nil {
		t.Fatalf("warm stop recovery = %v, want nil", err)
	}
	if _, kills, _ := units.counts(); kills != 0 {
		t.Errorf("kills = %d, want 0 for an already completed warm stop", kills)
	}
	next, err := configuration.nextMemorySnapshotGeneration("vm-1")
	if err != nil || next != 2 {
		t.Errorf("next generation = %d (err %v), want 2, so no second snapshot was made", next, err)
	}
}

// warmStopMachine builds a machine with a configuration and a fake API socket.
func warmStopMachine(t *testing.T, configuration Config, onRequest func()) *machine {
	t.Helper()
	return &machine{
		runtime:     &Runtime{configuration: configuration, units: &stubUnits{active: true}, serialBroker: &stubSerialBroker{}},
		input:       vm.RuntimeMachine{ID: "vm-1", UserID: 100001},
		api:         api.New(fcSocket(t, onRequest)),
		stopTimeout: time.Minute,
	}
}

func TestRemovePurgesMemorySnapshots(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), SocketsDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	runtime := &Runtime{configuration: configuration, units: &stubUnits{}, serialBroker: &stubSerialBroker{}}
	if err := os.MkdirAll(configuration.memorySnapshotGenerationDirectory("vm-1", 1), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := runtime.Remove(context.Background(), vm.RuntimeMachine{ID: "vm-1", UserID: 100001}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configuration.memorySnapshotRoot("vm-1")); !os.IsNotExist(err) {
		t.Error("Remove did not purge the snapshots")
	}
}

func TestStopPurgesMemorySnapshots(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), SocketsDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	units := &stubUnits{active: true}
	m := &machine{
		runtime:     &Runtime{configuration: configuration, units: units, serialBroker: &stubSerialBroker{}},
		input:       vm.RuntimeMachine{ID: "vm-1", UserID: 100001},
		api:         api.New(fcSocket(t, units.shutdown)),
		stopTimeout: time.Minute,
	}
	if err := os.MkdirAll(configuration.memorySnapshotGenerationDirectory("vm-1", 1), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configuration.memorySnapshotRoot("vm-1")); !os.IsNotExist(err) {
		t.Error("Stop did not purge the snapshots")
	}
}

func TestWarmStopRecoveryResumesAndRemovesPending(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	resumeRequested := false
	m := warmStopMachine(t, configuration, func() { resumeRequested = true })

	pending := configuration.pendingMemorySnapshotDirectory("vm-1")
	if err := os.MkdirAll(pending, 0o750); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("snapshot create failed")

	err := m.recoverFailedWarmStop(context.Background(), true, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want the original cause", err)
	}
	if !resumeRequested {
		t.Error("a VM this call paused must be resumed")
	}
	if _, statErr := os.Stat(pending); !os.IsNotExist(statErr) {
		t.Error("the pending directory was not removed")
	}
}

func TestWarmStopRecoveryLeavesAnOriginallyPausedVMPaused(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	resumeRequested := false
	m := warmStopMachine(t, configuration, func() { resumeRequested = true })

	err := m.recoverFailedWarmStop(context.Background(), false, errors.New("boom"))
	if err == nil {
		t.Fatal("want the original error")
	}
	if resumeRequested {
		t.Error("a VM this call did not pause must stay paused")
	}
}
