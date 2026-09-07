package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// createAndPublishSnapshot creates a Full snapshot of a paused VM and publishes
// it as a new generation. The guest must already be paused. The create step
// runs inside the jail, so it needs the daemon privileges. Warm image building
// and warm stop share this path.
func (runtime *Runtime) createAndPublishSnapshot(ctx context.Context, machine vm.RuntimeMachine) (validatedSnapshot, error) {
	generation, err := runtime.configuration.nextSnapshotGeneration(machine.ID)
	if err != nil {
		return validatedSnapshot{}, err
	}
	if _, _, err := runtime.createFullSnapshot(ctx, machine, pendingSnapshotDirName); err != nil {
		return validatedSnapshot{}, err
	}
	return runtime.publishPendingSnapshot(machine, generation)
}

// publishPendingSnapshot moves a complete pending directory to a new published
// generation and writes the manifest last, so a generation is complete only
// when its manifest validates. It reads the published result back to confirm it.
func (runtime *Runtime) publishPendingSnapshot(machine vm.RuntimeMachine, generation uint64) (validatedSnapshot, error) {
	pending := runtime.configuration.pendingSnapshotDirectory(machine.ID)
	stateSize, err := statArtifact(filepath.Join(pending, snapshotStateFileName))
	if err != nil {
		return validatedSnapshot{}, err
	}
	memorySize, err := statArtifact(filepath.Join(pending, snapshotMemoryFileName))
	if err != nil {
		return validatedSnapshot{}, err
	}

	generationDirectory := runtime.configuration.snapshotGenerationDirectory(machine.ID, generation)
	if err := os.MkdirAll(runtime.configuration.snapshotGenerationsDirectory(machine.ID), 0o750); err != nil {
		return validatedSnapshot{}, fmt.Errorf("create snapshot generations directory: %w", err)
	}
	if err := os.Rename(pending, generationDirectory); err != nil {
		return validatedSnapshot{}, fmt.Errorf("publish snapshot generation %d: %w", generation, err)
	}

	manifest := newSnapshotManifest(snapshotManifest{
		VirtualMachineID:         machine.ID,
		UserID:                   machine.UserID,
		SpecificationGeneration:  machine.SpecificationGeneration,
		RestartGeneration:        machine.RestartGeneration,
		FirecrackerCompatibility: runtime.firecrackerCompatibility(),
		CreatedAt:                time.Now().UTC(),
		StateFileSizeBytes:       stateSize,
		MemoryFileSizeBytes:      memorySize,
	})
	data, err := encodeSnapshotManifest(manifest)
	if err != nil {
		return validatedSnapshot{}, err
	}
	// The manifest is written last, so a reader never sees a partial generation.
	if err := platform.WriteFile(filepath.Join(generationDirectory, snapshotManifestFileName), data, 0o640); err != nil {
		return validatedSnapshot{}, fmt.Errorf("write snapshot manifest: %w", err)
	}

	return runtime.configuration.validateSnapshotGeneration(snapshotRequirement{
		VirtualMachineID:         machine.ID,
		UserID:                   machine.UserID,
		SpecificationGeneration:  machine.SpecificationGeneration,
		RestartGeneration:        machine.RestartGeneration,
		FirecrackerCompatibility: runtime.firecrackerCompatibility(),
	}, generation)
}

// statArtifact confirms a snapshot file is a regular, non-empty file and returns
// its size.
func statArtifact(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("stat snapshot artifact %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("snapshot artifact %s is not a regular file", filepath.Base(path))
	}
	if info.Size() == 0 {
		return 0, fmt.Errorf("snapshot artifact %s is empty", filepath.Base(path))
	}
	return info.Size(), nil
}
