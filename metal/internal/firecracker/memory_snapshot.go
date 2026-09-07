package firecracker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// Snapshot artifact file names. A full snapshot writes one state file and one
// memory file. Warm images and sleep snapshots use the same names.
const (
	snapshotStateFileName  = "state"
	snapshotMemoryFileName = "memory"
	// warmSnapshotDirName is the jail-relative directory for a warm image snapshot.
	warmSnapshotDirName = "warm"
)

// Compatibility returns the Firecracker build identity for warm images.
func (runtime *Runtime) Compatibility() string {
	return runtime.firecrackerCompatibility()
}

// CreateMemorySnapshot writes Firecracker state and memory files for a warm image.
func (runtime *Runtime) CreateMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (string, string, error) {
	return runtime.createFullSnapshot(ctx, machine, warmSnapshotDirName)
}

// createFullSnapshot writes a Full snapshot into a jail-relative directory and
// returns the host paths of the state and memory files. The guest must already
// be paused. Warm images and sleep snapshots share this primitive.
func (runtime *Runtime) createFullSnapshot(ctx context.Context, machine vm.RuntimeMachine, relativeDirectory string) (string, string, error) {
	directory := filepath.Join(runtime.configuration.chrootRoot(machine.ID), relativeDirectory)
	if err := mkdirChown(directory, machine.UserID, machine.GroupID); err != nil {
		return "", "", err
	}
	if err := api.New(runtime.configuration.socketPath(machine.ID)).CreateSnapshot(ctx, api.CreateSnapshotRequest{
		SnapshotType: "Full",
		SnapshotPath: filepath.Join(relativeDirectory, snapshotStateFileName),
		MemoryFile:   filepath.Join(relativeDirectory, snapshotMemoryFileName),
	}); err != nil {
		return "", "", fmt.Errorf("create full snapshot: %w", err)
	}
	return filepath.Join(directory, snapshotStateFileName), filepath.Join(directory, snapshotMemoryFileName), nil
}
