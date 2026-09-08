package firecracker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func validRequirement() memorySnapshotRequirement {
	return memorySnapshotRequirement{
		VirtualMachineID:         "vm-1",
		UserID:                   100001,
		SpecificationGeneration:  4,
		RestartGeneration:        1,
		FirecrackerCompatibility: "firecracker-1.16.1",
	}
}

// layDownGeneration writes a valid published generation matching completeManifest.
func layDownGeneration(t *testing.T, configuration Config, generation uint64) string {
	t.Helper()
	directory := configuration.memorySnapshotGenerationDirectory("vm-1", generation)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := completeManifest()
	writeFile(t, filepath.Join(directory, memorySnapshotStateFileName), int(manifest.StateFileSizeBytes))
	writeFile(t, filepath.Join(directory, memorySnapshotMemoryFileName), int(manifest.MemoryFileSizeBytes))
	data, err := encodeMemorySnapshotManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, memorySnapshotManifestFileName), data, 0o640); err != nil {
		t.Fatal(err)
	}
	return directory
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMemorySnapshotGenerationAcceptsAValidSnapshot(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	layDownGeneration(t, configuration, 4)

	got, err := configuration.validateMemorySnapshotGeneration(validRequirement(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 4 || got.StatePath == "" || got.MemoryPath == "" {
		t.Fatalf("validated snapshot = %+v", got)
	}
}

func TestLatestValidMemorySnapshotValidatesTheNewestGeneration(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	layDownGeneration(t, configuration, 2)
	layDownGeneration(t, configuration, 5)

	got, err := configuration.latestValidMemorySnapshot(validRequirement())
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 5 {
		t.Errorf("generation = %d, want 5", got.Generation)
	}
}

func TestLatestValidMemorySnapshotReportsNotFoundWithoutAGeneration(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}

	if _, err := configuration.latestValidMemorySnapshot(validRequirement()); !errors.Is(err, errMemorySnapshotNotFound) {
		t.Fatalf("error = %v, want errMemorySnapshotNotFound", err)
	}
}

func TestValidateMemorySnapshotGenerationReportsNotFoundWithoutAManifest(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	// Lay down the artifacts but remove the manifest, so the snapshot is absent.
	directory := layDownGeneration(t, configuration, 4)
	if err := os.Remove(filepath.Join(directory, memorySnapshotManifestFileName)); err != nil {
		t.Fatal(err)
	}

	_, err := configuration.validateMemorySnapshotGeneration(validRequirement(), 4)
	if !errors.Is(err, errMemorySnapshotNotFound) {
		t.Fatalf("error = %v, want errMemorySnapshotNotFound", err)
	}
}

func TestValidateMemorySnapshotGenerationRejectsInvalidSnapshots(t *testing.T) {
	cases := []struct {
		name        string
		requirement memorySnapshotRequirement
		mutate      func(t *testing.T, directory string)
	}{
		{name: "wrong compatibility", requirement: func() memorySnapshotRequirement {
			requirement := validRequirement()
			requirement.FirecrackerCompatibility = "firecracker-9.9.9"
			return requirement
		}()},
		{name: "wrong user id", requirement: func() memorySnapshotRequirement {
			requirement := validRequirement()
			requirement.UserID = 200000
			return requirement
		}()},
		{name: "wrong specification generation", requirement: func() memorySnapshotRequirement {
			requirement := validRequirement()
			requirement.SpecificationGeneration = 99
			return requirement
		}()},
		{name: "state file missing", requirement: validRequirement(), mutate: func(t *testing.T, directory string) {
			if err := os.Remove(filepath.Join(directory, memorySnapshotStateFileName)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "memory size mismatch", requirement: validRequirement(), mutate: func(t *testing.T, directory string) {
			writeFile(t, filepath.Join(directory, memorySnapshotMemoryFileName), 1234)
		}},
		{name: "state file is a symlink", requirement: validRequirement(), mutate: func(t *testing.T, directory string) {
			statePath := filepath.Join(directory, memorySnapshotStateFileName)
			if err := os.Remove(statePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(directory, memorySnapshotMemoryFileName), statePath); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := Config{MachinesDir: t.TempDir()}
			directory := layDownGeneration(t, configuration, 4)
			if testCase.mutate != nil {
				testCase.mutate(t, directory)
			}

			_, err := configuration.validateMemorySnapshotGeneration(testCase.requirement, 4)
			if err == nil {
				t.Fatal("want an invalid-artifact error")
			}
			if errors.Is(err, errMemorySnapshotNotFound) {
				t.Fatalf("error = %v, want an invalid-artifact error, not absence", err)
			}
		})
	}
}
