package firecracker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// Compatibility returns the Firecracker build identity for warm images.
func (runtime *Runtime) Compatibility() string {
	return runtime.firecrackerCompatibility()
}

// CreateMemorySnapshot writes Firecracker state and memory files.
func (runtime *Runtime) CreateMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (string, string, error) {
	directory := filepath.Join(runtime.configuration.chrootRoot(machine.ID), "warm")
	if err := mkdirChown(directory, machine.UserID, machine.GroupID); err != nil {
		return "", "", err
	}
	if err := api.New(runtime.configuration.socketPath(machine.ID)).CreateSnapshot(ctx, api.CreateSnapshotRequest{
		SnapshotType: "Full", SnapshotPath: "warm/state", MemoryFile: "warm/memory",
	}); err != nil {
		return "", "", fmt.Errorf("create warm memory snapshot: %w", err)
	}
	return filepath.Join(directory, "state"), filepath.Join(directory, "memory"), nil
}
