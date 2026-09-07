package firecracker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// Sleep snapshot layout below the VM directory. metald publishes one generation
// per warm stop and never overwrites a memory file a live process can map.
//
//	machines/<id>/sleep/generations/<n>/state
//	machines/<id>/sleep/generations/<n>/memory
//	machines/<id>/sleep/generations/<n>/manifest.json
const (
	sleepDirectoryName       = "sleep"
	sleepGenerationsDirName  = "generations"
	pendingSnapshotDirName   = "sleep-pending"
	snapshotManifestFileName = "manifest.json"
)

// sleepRoot is the VM-local root for every sleep artifact.
func (configuration Config) sleepRoot(id string) string {
	return filepath.Join(configuration.vmDir(id), sleepDirectoryName)
}

// sleepGenerationsDirectory holds one directory for each published generation.
func (configuration Config) sleepGenerationsDirectory(id string) string {
	return filepath.Join(configuration.sleepRoot(id), sleepGenerationsDirName)
}

// sleepGenerationDirectory is the published directory of one generation.
func (configuration Config) sleepGenerationDirectory(id string, generation uint64) string {
	return filepath.Join(configuration.sleepGenerationsDirectory(id), strconv.FormatUint(generation, 10))
}

// pendingSnapshotDirectory is where Firecracker writes a new generation inside
// its jail. metald moves it to a published generation directory when complete.
func (configuration Config) pendingSnapshotDirectory(id string) string {
	return filepath.Join(configuration.chrootRoot(id), pendingSnapshotDirName)
}

// nextSleepGeneration returns one more than the highest published generation. It
// returns 1 when none exist. It ignores a directory name that is not a
// canonical positive base-10 number.
func (configuration Config) nextSleepGeneration(id string) (uint64, error) {
	entries, err := os.ReadDir(configuration.sleepGenerationsDirectory(id))
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list sleep generations: %w", err)
	}

	var highest uint64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		generation, ok := parseSleepGeneration(entry.Name())
		if !ok {
			continue
		}
		if generation > highest {
			highest = generation
		}
	}
	return highest + 1, nil
}

// errSleepSnapshotNotFound reports that no complete snapshot exists for a
// generation. It is distinct from an invalid-artifact error, so a caller can
// tell absence from corruption.
var errSleepSnapshotNotFound = errors.New("sleep snapshot not found")

// sleepSnapshotRequirement is what a restore needs a published snapshot to be.
type sleepSnapshotRequirement struct {
	VirtualMachineID         string
	UserID                   uint32
	SpecificationGeneration  uint64
	RestartGeneration        uint64
	FirecrackerCompatibility string
}

// validatedSleepSnapshot is a snapshot proven complete and compatible.
type validatedSleepSnapshot struct {
	Generation uint64
	Manifest   sleepManifest
	StatePath  string
	MemoryPath string
}

// validateSleepGeneration validates the published snapshot of one generation. It
// returns errSleepSnapshotNotFound when the manifest is absent. A manifest that
// is present but does not match, or an artifact that is missing, wrong sized, or
// not a regular file, is an invalid-artifact error, not absence.
func (configuration Config) validateSleepGeneration(requirement sleepSnapshotRequirement, generation uint64) (validatedSleepSnapshot, error) {
	directory := configuration.sleepGenerationDirectory(requirement.VirtualMachineID, generation)
	data, err := os.ReadFile(filepath.Join(directory, snapshotManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return validatedSleepSnapshot{}, errSleepSnapshotNotFound
	}
	if err != nil {
		return validatedSleepSnapshot{}, fmt.Errorf("read sleep manifest: %w", err)
	}

	manifest, err := decodeSleepManifest(data)
	if err != nil {
		return validatedSleepSnapshot{}, err
	}
	if err := manifest.matches(requirement); err != nil {
		return validatedSleepSnapshot{}, err
	}

	statePath, err := validateArtifactFile(directory, snapshotStateFileName, manifest.StateFileSizeBytes)
	if err != nil {
		return validatedSleepSnapshot{}, err
	}
	memoryPath, err := validateArtifactFile(directory, snapshotMemoryFileName, manifest.MemoryFileSizeBytes)
	if err != nil {
		return validatedSleepSnapshot{}, err
	}

	return validatedSleepSnapshot{Generation: generation, Manifest: manifest, StatePath: statePath, MemoryPath: memoryPath}, nil
}

// matches reports whether the manifest describes the required VM and build.
func (manifest sleepManifest) matches(requirement sleepSnapshotRequirement) error {
	switch {
	case manifest.VirtualMachineID != requirement.VirtualMachineID:
		return fmt.Errorf("sleep manifest VM ID %q does not match %q", manifest.VirtualMachineID, requirement.VirtualMachineID)
	case manifest.UserID != requirement.UserID:
		return fmt.Errorf("sleep manifest user ID %d does not match %d", manifest.UserID, requirement.UserID)
	case manifest.SpecificationGeneration != requirement.SpecificationGeneration:
		return fmt.Errorf("sleep manifest specification generation %d does not match %d", manifest.SpecificationGeneration, requirement.SpecificationGeneration)
	case manifest.RestartGeneration != requirement.RestartGeneration:
		return fmt.Errorf("sleep manifest restart generation %d does not match %d", manifest.RestartGeneration, requirement.RestartGeneration)
	case manifest.FirecrackerCompatibility != requirement.FirecrackerCompatibility:
		return fmt.Errorf("sleep manifest Firecracker compatibility %q does not match %q", manifest.FirecrackerCompatibility, requirement.FirecrackerCompatibility)
	case manifest.StateFileName != snapshotStateFileName || manifest.MemoryFileName != snapshotMemoryFileName:
		return fmt.Errorf("sleep manifest uses unexpected artifact file names")
	case manifest.StateFileSizeBytes <= 0 || manifest.MemoryFileSizeBytes <= 0:
		return fmt.Errorf("sleep manifest has a non-positive file size")
	}
	return nil
}

// validateArtifactFile checks one snapshot file. It derives the path from the
// fixed name, never from the manifest, so a bad manifest cannot redirect it.
func validateArtifactFile(directory, fixedName string, expectedSize int64) (string, error) {
	path := filepath.Join(directory, fixedName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("sleep snapshot %s is unreadable: %w", fixedName, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("sleep snapshot %s is not a regular file", fixedName)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("sleep snapshot %s is empty", fixedName)
	}
	if info.Size() != expectedSize {
		return "", fmt.Errorf("sleep snapshot %s size %d does not match the manifest %d", fixedName, info.Size(), expectedSize)
	}
	return path, nil
}

// parseSleepGeneration accepts a canonical positive base-10 generation name. It
// rejects zero and a name with leading zeros, so one generation has one name.
func parseSleepGeneration(name string) (uint64, bool) {
	generation, err := strconv.ParseUint(name, 10, 64)
	if err != nil || generation == 0 {
		return 0, false
	}
	if strconv.FormatUint(generation, 10) != name {
		return 0, false
	}
	return generation, true
}
