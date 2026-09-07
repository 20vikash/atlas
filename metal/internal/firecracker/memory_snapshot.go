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
)

// Compatibility returns the Firecracker build identity for warm images.
func (runtime *Runtime) Compatibility() string {
	return runtime.firecrackerCompatibility()
}

// CreateMemorySnapshot publishes a first-class VM-local snapshot and returns its
// state and memory file paths. The warm image builder promotes those files into
// the image store. The returned interface is unchanged, so warm image behavior
// is the same as before.
func (runtime *Runtime) CreateMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (string, string, error) {
	published, err := runtime.createAndPublishSnapshot(ctx, machine)
	if err != nil {
		return "", "", err
	}
	return published.StatePath, published.MemoryPath, nil
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
