package firecracker

import (
	"os"
	"path/filepath"
	"testing"

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
