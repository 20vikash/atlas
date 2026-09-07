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

// writePendingSnapshot simulates Firecracker having written a pending snapshot
// into the jail.
func writePendingSnapshot(t *testing.T, configuration Config, id string, stateSize, memorySize int) {
	t.Helper()
	pending := configuration.pendingSnapshotDirectory(id)
	if err := os.MkdirAll(pending, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pending, snapshotStateFileName), stateSize)
	writeFile(t, filepath.Join(pending, snapshotMemoryFileName), memorySize)
}

func TestPublishPendingSnapshotMovesAndValidates(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	runtime := &Runtime{configuration: configuration}
	machine := vm.RuntimeMachine{ID: "vm-1", UserID: 100001, SpecificationGeneration: 4, RestartGeneration: 1}
	writePendingSnapshot(t, configuration, "vm-1", 2048, 4096)

	published, err := runtime.publishPendingSnapshot(machine, 1)
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
	if _, err := os.Stat(configuration.pendingSnapshotDirectory("vm-1")); !os.IsNotExist(err) {
		t.Error("pending directory was not moved")
	}
	// The published generation holds all three files.
	generationDirectory := configuration.snapshotGenerationDirectory("vm-1", 1)
	for _, name := range []string{snapshotStateFileName, snapshotMemoryFileName, snapshotManifestFileName} {
		if _, err := os.Stat(filepath.Join(generationDirectory, name)); err != nil {
			t.Errorf("published generation is missing %s: %v", name, err)
		}
	}
}

func TestPublishPendingSnapshotRejectsAnIncompletePending(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	runtime := &Runtime{configuration: configuration}
	machine := vm.RuntimeMachine{ID: "vm-1", UserID: 100001}
	// Create the pending directory with no artifact files.
	if err := os.MkdirAll(configuration.pendingSnapshotDirectory("vm-1"), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.publishPendingSnapshot(machine, 1); err == nil {
		t.Fatal("want an error for a missing artifact")
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

func TestRemovePurgesSnapshots(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), SocketsDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	runtime := &Runtime{configuration: configuration, units: &stubUnits{}, serialBroker: &stubSerialBroker{}}
	if err := os.MkdirAll(configuration.snapshotGenerationDirectory("vm-1", 1), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := runtime.Remove(context.Background(), vm.RuntimeMachine{ID: "vm-1", UserID: 100001}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configuration.snapshotRoot("vm-1")); !os.IsNotExist(err) {
		t.Error("Remove did not purge the snapshots")
	}
}

func TestStopPurgesSnapshots(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), SocketsDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	units := &stubUnits{active: true}
	m := &machine{
		runtime:     &Runtime{configuration: configuration, units: units, serialBroker: &stubSerialBroker{}},
		input:       vm.RuntimeMachine{ID: "vm-1", UserID: 100001},
		api:         api.New(fcSocket(t, units.shutdown)),
		stopTimeout: time.Minute,
	}
	if err := os.MkdirAll(configuration.snapshotGenerationDirectory("vm-1", 1), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configuration.snapshotRoot("vm-1")); !os.IsNotExist(err) {
		t.Error("Stop did not purge the snapshots")
	}
}

func TestWarmStopRecoveryResumesAndRemovesPending(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir(), FirecrackerBin: "/usr/bin/firecracker"}
	resumeRequested := false
	m := warmStopMachine(t, configuration, func() { resumeRequested = true })

	pending := configuration.pendingSnapshotDirectory("vm-1")
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
