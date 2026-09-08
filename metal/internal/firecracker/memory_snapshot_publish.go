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

// createAndPublishMemorySnapshot creates and publishes a full snapshot of a
// paused VM. Warm image creation and warm stop share this path.
func (runtime *Runtime) createAndPublishMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (validatedMemorySnapshot, error) {
	generation, err := runtime.configuration.nextMemorySnapshotGeneration(machine.ID)
	if err != nil {
		return validatedMemorySnapshot{}, err
	}
	if _, _, err := runtime.createFullMemorySnapshot(ctx, machine, pendingMemorySnapshotDirectoryName); err != nil {
		return validatedMemorySnapshot{}, err
	}
	return runtime.publishPendingMemorySnapshot(machine, generation)
}

// publishPendingMemorySnapshot moves a complete pending directory to a new
// generation and writes the manifest last.
func (runtime *Runtime) publishPendingMemorySnapshot(machine vm.RuntimeMachine, generation uint64) (validatedMemorySnapshot, error) {
	pending := runtime.configuration.pendingMemorySnapshotDirectory(machine.ID)
	stateSize, err := statMemorySnapshotFile(filepath.Join(pending, memorySnapshotStateFileName))
	if err != nil {
		return validatedMemorySnapshot{}, err
	}
	memorySize, err := statMemorySnapshotFile(filepath.Join(pending, memorySnapshotMemoryFileName))
	if err != nil {
		return validatedMemorySnapshot{}, err
	}

	generationDirectory := runtime.configuration.memorySnapshotGenerationDirectory(machine.ID, generation)
	if err := os.MkdirAll(runtime.configuration.memorySnapshotRoot(machine.ID), 0o750); err != nil {
		return validatedMemorySnapshot{}, fmt.Errorf("create memory snapshot directory: %w", err)
	}
	if err := os.Rename(pending, generationDirectory); err != nil {
		return validatedMemorySnapshot{}, fmt.Errorf("publish memory snapshot generation %d: %w", generation, err)
	}

	manifest := newMemorySnapshotManifest(memorySnapshotManifest{
		VirtualMachineID:         machine.ID,
		UserID:                   machine.UserID,
		SpecificationGeneration:  machine.SpecificationGeneration,
		RestartGeneration:        machine.RestartGeneration,
		FirecrackerCompatibility: runtime.firecrackerCompatibility(),
		CreatedAt:                time.Now().UTC(),
		StateFileSizeBytes:       stateSize,
		MemoryFileSizeBytes:      memorySize,
	})
	data, err := encodeMemorySnapshotManifest(manifest)
	if err != nil {
		return validatedMemorySnapshot{}, err
	}
	// Write the manifest last, so readers never accept a partial generation.
	if err := platform.WriteFile(filepath.Join(generationDirectory, memorySnapshotManifestFileName), data, 0o640); err != nil {
		return validatedMemorySnapshot{}, fmt.Errorf("write memory snapshot manifest: %w", err)
	}

	return runtime.configuration.validateMemorySnapshotGeneration(memorySnapshotRequirement{
		VirtualMachineID:         machine.ID,
		UserID:                   machine.UserID,
		SpecificationGeneration:  machine.SpecificationGeneration,
		RestartGeneration:        machine.RestartGeneration,
		FirecrackerCompatibility: runtime.firecrackerCompatibility(),
	}, generation)
}

// purgeMemorySnapshots removes every published memory snapshot of one VM. An
// absent directory is allowed.
func (runtime *Runtime) purgeMemorySnapshots(id string) error {
	if err := os.RemoveAll(runtime.configuration.memorySnapshotRoot(id)); err != nil {
		return fmt.Errorf("remove VM memory snapshots: %w", err)
	}
	return nil
}

// statMemorySnapshotFile validates one regular, non-empty snapshot file and
// returns its size.
func statMemorySnapshotFile(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("stat memory snapshot file %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("memory snapshot file %s is not a regular file", filepath.Base(path))
	}
	if info.Size() == 0 {
		return 0, fmt.Errorf("memory snapshot file %s is empty", filepath.Base(path))
	}
	return info.Size(), nil
}
