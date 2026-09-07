package firecracker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func validRequirement() snapshotRequirement {
	return snapshotRequirement{
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
	directory := configuration.snapshotGenerationDirectory("vm-1", generation)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := completeManifest()
	writeFile(t, filepath.Join(directory, snapshotStateFileName), int(manifest.StateFileSizeBytes))
	writeFile(t, filepath.Join(directory, snapshotMemoryFileName), int(manifest.MemoryFileSizeBytes))
	data, err := encodeSnapshotManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, snapshotManifestFileName), data, 0o640); err != nil {
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

func TestValidateSnapshotGenerationAcceptsAValidSnapshot(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	layDownGeneration(t, configuration, 4)

	got, err := configuration.validateSnapshotGeneration(validRequirement(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 4 || got.StatePath == "" || got.MemoryPath == "" {
		t.Fatalf("validated snapshot = %+v", got)
	}
}

func TestValidateSnapshotGenerationReportsNotFoundWithoutAManifest(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	// Lay down the artifacts but remove the manifest, so the snapshot is absent.
	directory := layDownGeneration(t, configuration, 4)
	if err := os.Remove(filepath.Join(directory, snapshotManifestFileName)); err != nil {
		t.Fatal(err)
	}

	_, err := configuration.validateSnapshotGeneration(validRequirement(), 4)
	if !errors.Is(err, errSnapshotNotFound) {
		t.Fatalf("error = %v, want errSnapshotNotFound", err)
	}
}

func TestValidateSnapshotGenerationRejectsInvalidSnapshots(t *testing.T) {
	cases := []struct {
		name        string
		requirement snapshotRequirement
		mutate      func(t *testing.T, directory string)
	}{
		{name: "wrong compatibility", requirement: func() snapshotRequirement {
			requirement := validRequirement()
			requirement.FirecrackerCompatibility = "firecracker-9.9.9"
			return requirement
		}()},
		{name: "wrong user id", requirement: func() snapshotRequirement {
			requirement := validRequirement()
			requirement.UserID = 200000
			return requirement
		}()},
		{name: "wrong specification generation", requirement: func() snapshotRequirement {
			requirement := validRequirement()
			requirement.SpecificationGeneration = 99
			return requirement
		}()},
		{name: "state file missing", requirement: validRequirement(), mutate: func(t *testing.T, directory string) {
			if err := os.Remove(filepath.Join(directory, snapshotStateFileName)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "memory size mismatch", requirement: validRequirement(), mutate: func(t *testing.T, directory string) {
			writeFile(t, filepath.Join(directory, snapshotMemoryFileName), 1234)
		}},
		{name: "state file is a symlink", requirement: validRequirement(), mutate: func(t *testing.T, directory string) {
			statePath := filepath.Join(directory, snapshotStateFileName)
			if err := os.Remove(statePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(directory, snapshotMemoryFileName), statePath); err != nil {
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

			_, err := configuration.validateSnapshotGeneration(testCase.requirement, 4)
			if err == nil {
				t.Fatal("want an invalid-artifact error")
			}
			if errors.Is(err, errSnapshotNotFound) {
				t.Fatalf("error = %v, want an invalid-artifact error, not absence", err)
			}
		})
	}
}
