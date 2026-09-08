package firecracker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// Memory snapshot file names shared by warm images and sleep snapshots. A full
// snapshot contains one state file and one memory file.
const (
	memorySnapshotStateFileName  = "state"
	memorySnapshotMemoryFileName = "memory"
)

// Compatibility returns the Firecracker build identity for warm images.
func (runtime *Runtime) Compatibility() string {
	return runtime.firecrackerCompatibility()
}

// CreateMemorySnapshot publishes a VM-local snapshot and returns its file paths.
// Warm image creation promotes those files into image storage.
func (runtime *Runtime) CreateMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (string, string, error) {
	published, err := runtime.createAndPublishMemorySnapshot(ctx, machine)
	if err != nil {
		return "", "", err
	}
	return published.StatePath, published.MemoryPath, nil
}

// createFullMemorySnapshot writes a full snapshot of an already paused guest
// into a jail-relative directory.
func (runtime *Runtime) createFullMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine, relativeDirectory string) (string, string, error) {
	directory := filepath.Join(runtime.configuration.chrootRoot(machine.ID), relativeDirectory)
	if err := mkdirChown(directory, machine.UserID, machine.GroupID); err != nil {
		return "", "", err
	}
	if err := api.New(runtime.configuration.socketPath(machine.ID)).CreateSnapshot(ctx, api.CreateSnapshotRequest{
		SnapshotType: "Full",
		SnapshotPath: filepath.Join(relativeDirectory, memorySnapshotStateFileName),
		MemoryFile:   filepath.Join(relativeDirectory, memorySnapshotMemoryFileName),
	}); err != nil {
		return "", "", fmt.Errorf("create full memory snapshot: %w", err)
	}
	return filepath.Join(directory, memorySnapshotStateFileName), filepath.Join(directory, memorySnapshotMemoryFileName), nil
}
