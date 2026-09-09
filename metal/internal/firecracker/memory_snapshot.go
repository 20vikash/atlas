package firecracker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// Memory snapshot file names are shared by warm images and saved VM state.
const (
	memorySnapshotStateFileName  = "state"
	memorySnapshotMemoryFileName = "memory"
)

// Compatibility returns the Firecracker build identity for warm images.
func (runtime *Runtime) Compatibility() string {
	return runtime.firecrackerCompatibility()
}

// CreateMemorySnapshot writes files for one warm image.
func (runtime *Runtime) CreateMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (string, string, error) {
	return runtime.createFullMemorySnapshot(ctx, machine, "warm")
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
