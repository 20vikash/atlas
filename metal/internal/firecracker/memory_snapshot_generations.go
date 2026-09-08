package firecracker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// Memory snapshot layout below the VM directory.
//
//	machines/<id>/memory-snapshots/<n>/state
//	machines/<id>/memory-snapshots/<n>/memory
//	machines/<id>/memory-snapshots/<n>/manifest.json
const (
	memorySnapshotDirectoryName        = "memory-snapshots"
	pendingMemorySnapshotDirectoryName = "memory-snapshot-pending"
	memorySnapshotManifestFileName     = "manifest.json"
)

// memorySnapshotRoot holds one directory for each published generation.
func (configuration Config) memorySnapshotRoot(id string) string {
	return filepath.Join(configuration.vmDir(id), memorySnapshotDirectoryName)
}

// memorySnapshotGenerationDirectory is the published directory of one generation.
func (configuration Config) memorySnapshotGenerationDirectory(id string, generation uint64) string {
	return filepath.Join(configuration.memorySnapshotRoot(id), strconv.FormatUint(generation, 10))
}

// pendingMemorySnapshotDirectory is where Firecracker writes a new generation.
func (configuration Config) pendingMemorySnapshotDirectory(id string) string {
	return filepath.Join(configuration.chrootRoot(id), pendingMemorySnapshotDirectoryName)
}

// latestMemorySnapshotGeneration returns the highest valid generation.
func (configuration Config) latestMemorySnapshotGeneration(id string) (uint64, bool, error) {
	entries, err := os.ReadDir(configuration.memorySnapshotRoot(id))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("list memory snapshot generations: %w", err)
	}

	var highest uint64
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		generation, ok := parseMemorySnapshotGeneration(entry.Name())
		if !ok {
			continue
		}
		if generation > highest {
			highest = generation
			found = true
		}
	}
	return highest, found, nil
}

// nextMemorySnapshotGeneration returns the next generation, starting at 1.
func (configuration Config) nextMemorySnapshotGeneration(id string) (uint64, error) {
	highest, _, err := configuration.latestMemorySnapshotGeneration(id)
	if err != nil {
		return 0, err
	}
	return highest + 1, nil
}

// errMemorySnapshotNotFound reports that no complete memory snapshot exists.
var errMemorySnapshotNotFound = errors.New("memory snapshot not found")

// memorySnapshotRequirement describes a required memory snapshot.
type memorySnapshotRequirement struct {
	VirtualMachineID         string
	UserID                   uint32
	SpecificationGeneration  uint64
	RestartGeneration        uint64
	FirecrackerCompatibility string
}

// validatedMemorySnapshot is a complete, compatible memory snapshot.
type validatedMemorySnapshot struct {
	Generation uint64
	Manifest   memorySnapshotManifest
	StatePath  string
	MemoryPath string
}

// latestValidMemorySnapshot validates the newest published memory snapshot.
func (configuration Config) latestValidMemorySnapshot(requirement memorySnapshotRequirement) (validatedMemorySnapshot, error) {
	generation, found, err := configuration.latestMemorySnapshotGeneration(requirement.VirtualMachineID)
	if err != nil {
		return validatedMemorySnapshot{}, err
	}
	if !found {
		return validatedMemorySnapshot{}, errMemorySnapshotNotFound
	}

	return configuration.validateMemorySnapshotGeneration(requirement, generation)
}

// validateMemorySnapshotGeneration validates one published memory snapshot generation.
func (configuration Config) validateMemorySnapshotGeneration(requirement memorySnapshotRequirement, generation uint64) (validatedMemorySnapshot, error) {
	directory := configuration.memorySnapshotGenerationDirectory(requirement.VirtualMachineID, generation)
	data, err := os.ReadFile(filepath.Join(directory, memorySnapshotManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return validatedMemorySnapshot{}, errMemorySnapshotNotFound
	}
	if err != nil {
		return validatedMemorySnapshot{}, fmt.Errorf("read memory snapshot manifest: %w", err)
	}

	manifest, err := decodeMemorySnapshotManifest(data)
	if err != nil {
		return validatedMemorySnapshot{}, err
	}
	if err := manifest.matches(requirement); err != nil {
		return validatedMemorySnapshot{}, err
	}

	statePath, err := validateMemorySnapshotFile(directory, memorySnapshotStateFileName, manifest.StateFileSizeBytes)
	if err != nil {
		return validatedMemorySnapshot{}, err
	}
	memoryPath, err := validateMemorySnapshotFile(directory, memorySnapshotMemoryFileName, manifest.MemoryFileSizeBytes)
	if err != nil {
		return validatedMemorySnapshot{}, err
	}

	return validatedMemorySnapshot{Generation: generation, Manifest: manifest, StatePath: statePath, MemoryPath: memoryPath}, nil
}

// matches reports whether the manifest describes the required VM and build.
func (manifest memorySnapshotManifest) matches(requirement memorySnapshotRequirement) error {
	switch {
	case manifest.VirtualMachineID != requirement.VirtualMachineID:
		return fmt.Errorf("memory snapshot manifest VM ID %q does not match %q", manifest.VirtualMachineID, requirement.VirtualMachineID)
	case manifest.UserID != requirement.UserID:
		return fmt.Errorf("memory snapshot manifest user ID %d does not match %d", manifest.UserID, requirement.UserID)
	case manifest.SpecificationGeneration != requirement.SpecificationGeneration:
		return fmt.Errorf("memory snapshot manifest specification generation %d does not match %d", manifest.SpecificationGeneration, requirement.SpecificationGeneration)
	case manifest.RestartGeneration != requirement.RestartGeneration:
		return fmt.Errorf("memory snapshot manifest restart generation %d does not match %d", manifest.RestartGeneration, requirement.RestartGeneration)
	case manifest.FirecrackerCompatibility != requirement.FirecrackerCompatibility:
		return fmt.Errorf("memory snapshot manifest Firecracker compatibility %q does not match %q", manifest.FirecrackerCompatibility, requirement.FirecrackerCompatibility)
	case manifest.StateFileName != memorySnapshotStateFileName || manifest.MemoryFileName != memorySnapshotMemoryFileName:
		return fmt.Errorf("memory snapshot manifest uses unexpected artifact file names")
	case manifest.StateFileSizeBytes <= 0 || manifest.MemoryFileSizeBytes <= 0:
		return fmt.Errorf("memory snapshot manifest has a non-positive file size")
	}
	return nil
}

// validateMemorySnapshotFile checks one fixed-name memory snapshot file.
func validateMemorySnapshotFile(directory, fixedName string, expectedSize int64) (string, error) {
	path := filepath.Join(directory, fixedName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("memory snapshot %s is unreadable: %w", fixedName, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("memory snapshot %s is not a regular file", fixedName)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("memory snapshot %s is empty", fixedName)
	}
	if info.Size() != expectedSize {
		return "", fmt.Errorf("memory snapshot %s size %d does not match the manifest %d", fixedName, info.Size(), expectedSize)
	}
	return path, nil
}

// parseMemorySnapshotGeneration accepts a canonical positive generation name.
func parseMemorySnapshotGeneration(name string) (uint64, bool) {
	generation, err := strconv.ParseUint(name, 10, 64)
	if err != nil || generation == 0 {
		return 0, false
	}
	if strconv.FormatUint(generation, 10) != name {
		return 0, false
	}
	return generation, true
}
