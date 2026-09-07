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
	snapshotStateFileName    = "state"
	snapshotMemoryFileName   = "memory"
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
