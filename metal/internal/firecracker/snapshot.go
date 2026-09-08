package firecracker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// Snapshot layout below the VM directory.
//
//	machines/<id>/snapshots/generations/<n>/state
//	machines/<id>/snapshots/generations/<n>/memory
//	machines/<id>/snapshots/generations/<n>/manifest.json
const (
	snapshotDirectoryName      = "snapshots"
	snapshotGenerationsDirName = "generations"
	pendingSnapshotDirName     = "snapshot-pending"
	snapshotManifestFileName   = "manifest.json"
)

// snapshotRoot is the VM-local root for every snapshot artifact.
func (configuration Config) snapshotRoot(id string) string {
	return filepath.Join(configuration.vmDir(id), snapshotDirectoryName)
}

// snapshotGenerationsDirectory holds one directory for each published generation.
func (configuration Config) snapshotGenerationsDirectory(id string) string {
	return filepath.Join(configuration.snapshotRoot(id), snapshotGenerationsDirName)
}

// snapshotGenerationDirectory is the published directory of one generation.
func (configuration Config) snapshotGenerationDirectory(id string, generation uint64) string {
	return filepath.Join(configuration.snapshotGenerationsDirectory(id), strconv.FormatUint(generation, 10))
}

// pendingSnapshotDirectory is where Firecracker writes a new generation.
func (configuration Config) pendingSnapshotDirectory(id string) string {
	return filepath.Join(configuration.chrootRoot(id), pendingSnapshotDirName)
}

// latestSnapshotGeneration returns the highest valid generation.
func (configuration Config) latestSnapshotGeneration(id string) (uint64, bool, error) {
	entries, err := os.ReadDir(configuration.snapshotGenerationsDirectory(id))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("list snapshot generations: %w", err)
	}

	var highest uint64
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		generation, ok := parseSnapshotGeneration(entry.Name())
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

// nextSnapshotGeneration returns one more than the highest published generation.
// It returns 1 when none exist.
func (configuration Config) nextSnapshotGeneration(id string) (uint64, error) {
	highest, _, err := configuration.latestSnapshotGeneration(id)
	if err != nil {
		return 0, err
	}
	return highest + 1, nil
}

// errSnapshotNotFound reports that no complete snapshot exists.
var errSnapshotNotFound = errors.New("snapshot not found")

// snapshotRequirement is what a restore needs a published snapshot to be.
type snapshotRequirement struct {
	VirtualMachineID         string
	UserID                   uint32
	SpecificationGeneration  uint64
	RestartGeneration        uint64
	FirecrackerCompatibility string
}

// validatedSnapshot is a snapshot proven complete and compatible.
type validatedSnapshot struct {
	Generation uint64
	Manifest   snapshotManifest
	StatePath  string
	MemoryPath string
}

// latestValidSnapshot validates the newest published snapshot.
func (configuration Config) latestValidSnapshot(requirement snapshotRequirement) (validatedSnapshot, error) {
	generation, found, err := configuration.latestSnapshotGeneration(requirement.VirtualMachineID)
	if err != nil {
		return validatedSnapshot{}, err
	}
	if !found {
		return validatedSnapshot{}, errSnapshotNotFound
	}

	return configuration.validateSnapshotGeneration(requirement, generation)
}

// validateSnapshotGeneration validates one published snapshot generation.
func (configuration Config) validateSnapshotGeneration(requirement snapshotRequirement, generation uint64) (validatedSnapshot, error) {
	directory := configuration.snapshotGenerationDirectory(requirement.VirtualMachineID, generation)
	data, err := os.ReadFile(filepath.Join(directory, snapshotManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return validatedSnapshot{}, errSnapshotNotFound
	}
	if err != nil {
		return validatedSnapshot{}, fmt.Errorf("read snapshot manifest: %w", err)
	}

	manifest, err := decodeSnapshotManifest(data)
	if err != nil {
		return validatedSnapshot{}, err
	}
	if err := manifest.matches(requirement); err != nil {
		return validatedSnapshot{}, err
	}

	statePath, err := validateArtifactFile(directory, snapshotStateFileName, manifest.StateFileSizeBytes)
	if err != nil {
		return validatedSnapshot{}, err
	}
	memoryPath, err := validateArtifactFile(directory, snapshotMemoryFileName, manifest.MemoryFileSizeBytes)
	if err != nil {
		return validatedSnapshot{}, err
	}

	return validatedSnapshot{Generation: generation, Manifest: manifest, StatePath: statePath, MemoryPath: memoryPath}, nil
}

// matches reports whether the manifest describes the required VM and build.
func (manifest snapshotManifest) matches(requirement snapshotRequirement) error {
	switch {
	case manifest.VirtualMachineID != requirement.VirtualMachineID:
		return fmt.Errorf("snapshot manifest VM ID %q does not match %q", manifest.VirtualMachineID, requirement.VirtualMachineID)
	case manifest.UserID != requirement.UserID:
		return fmt.Errorf("snapshot manifest user ID %d does not match %d", manifest.UserID, requirement.UserID)
	case manifest.SpecificationGeneration != requirement.SpecificationGeneration:
		return fmt.Errorf("snapshot manifest specification generation %d does not match %d", manifest.SpecificationGeneration, requirement.SpecificationGeneration)
	case manifest.RestartGeneration != requirement.RestartGeneration:
		return fmt.Errorf("snapshot manifest restart generation %d does not match %d", manifest.RestartGeneration, requirement.RestartGeneration)
	case manifest.FirecrackerCompatibility != requirement.FirecrackerCompatibility:
		return fmt.Errorf("snapshot manifest Firecracker compatibility %q does not match %q", manifest.FirecrackerCompatibility, requirement.FirecrackerCompatibility)
	case manifest.StateFileName != snapshotStateFileName || manifest.MemoryFileName != snapshotMemoryFileName:
		return fmt.Errorf("snapshot manifest uses unexpected artifact file names")
	case manifest.StateFileSizeBytes <= 0 || manifest.MemoryFileSizeBytes <= 0:
		return fmt.Errorf("snapshot manifest has a non-positive file size")
	}
	return nil
}

// validateArtifactFile checks one fixed-name snapshot file.
func validateArtifactFile(directory, fixedName string, expectedSize int64) (string, error) {
	path := filepath.Join(directory, fixedName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("snapshot %s is unreadable: %w", fixedName, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("snapshot %s is not a regular file", fixedName)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("snapshot %s is empty", fixedName)
	}
	if info.Size() != expectedSize {
		return "", fmt.Errorf("snapshot %s size %d does not match the manifest %d", fixedName, info.Size(), expectedSize)
	}
	return path, nil
}

// parseSnapshotGeneration accepts a canonical positive generation name.
func parseSnapshotGeneration(name string) (uint64, bool) {
	generation, err := strconv.ParseUint(name, 10, 64)
	if err != nil || generation == 0 {
		return 0, false
	}
	if strconv.FormatUint(generation, 10) != name {
		return 0, false
	}
	return generation, true
}
