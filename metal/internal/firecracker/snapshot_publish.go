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

// createAndPublishSnapshot creates and publishes a paused VM snapshot.
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

// publishPendingSnapshot publishes a pending generation and writes its manifest.
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
	// Write the manifest last.
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

// purgeSnapshots removes every published snapshot of one VM.
func (runtime *Runtime) purgeSnapshots(id string) error {
	if err := os.RemoveAll(runtime.configuration.snapshotRoot(id)); err != nil {
		return fmt.Errorf("remove VM snapshots: %w", err)
	}
	return nil
}

// statArtifact validates a snapshot file and returns its size.
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
