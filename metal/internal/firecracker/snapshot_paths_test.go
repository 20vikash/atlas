package firecracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotPathsStayBelowTheVMDirectory(t *testing.T) {
	configuration := Config{MachinesDir: "/var/lib/metal/machines", FirecrackerBin: "/usr/bin/firecracker"}
	base := configuration.vmDir("vm-1") + string(filepath.Separator)
	paths := []string{
		configuration.snapshotRoot("vm-1"),
		configuration.snapshotGenerationsDirectory("vm-1"),
		configuration.snapshotGenerationDirectory("vm-1", 3),
		configuration.pendingSnapshotDirectory("vm-1"),
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, base) {
			t.Errorf("path %q is not below %q", path, base)
		}
	}
}

func TestNextSnapshotGenerationStartsAtOne(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	generation, err := configuration.nextSnapshotGeneration("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if generation != 1 {
		t.Errorf("generation = %d, want 1", generation)
	}
}

func TestNextSnapshotGenerationUsesTheHighestValidName(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	generations := configuration.snapshotGenerationsDirectory("vm-1")
	// "01", "junk", and "0" are not canonical positive numbers and are ignored.
	for _, name := range []string{"1", "2", "5", "01", "junk", "0"} {
		if err := os.MkdirAll(filepath.Join(generations, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	generation, err := configuration.nextSnapshotGeneration("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if generation != 6 {
		t.Errorf("generation = %d, want 6", generation)
	}
}

func TestLatestSnapshotGenerationReportsAbsence(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	if _, found, err := configuration.latestSnapshotGeneration("vm-1"); err != nil || found {
		t.Fatalf("latest = (found %v, err %v), want not found", found, err)
	}
}

func TestLatestSnapshotGenerationReturnsTheHighestValidName(t *testing.T) {
	configuration := Config{MachinesDir: t.TempDir()}
	generations := configuration.snapshotGenerationsDirectory("vm-1")
	for _, name := range []string{"1", "4", "2", "07", "junk"} {
		if err := os.MkdirAll(filepath.Join(generations, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	generation, found, err := configuration.latestSnapshotGeneration("vm-1")
	if err != nil || !found {
		t.Fatalf("latest = (found %v, err %v)", found, err)
	}
	if generation != 4 {
		t.Errorf("generation = %d, want 4", generation)
	}
}
