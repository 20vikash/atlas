package storage

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// Capacity describes total and available pool space in MiB.
type Capacity struct {
	TotalMiB     uint64
	AvailableMiB uint64
}

// Capacity returns the storage pool capacity.
func (pool *ZFSPool) Capacity(ctx context.Context) (Capacity, error) {
	output, err := platform.Output(ctx, "zpool", "list", "-Hp", "-o", "size,free", pool.name)
	if err != nil {
		return Capacity{}, fmt.Errorf("read storage pool capacity: %w", err)
	}
	fields := strings.Fields(output)
	if len(fields) != 2 {
		return Capacity{}, fmt.Errorf("storage pool returned invalid capacity data")
	}

	total, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return Capacity{}, fmt.Errorf("parse storage pool size: %w", err)
	}
	available, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return Capacity{}, fmt.Errorf("parse storage pool free space: %w", err)
	}
	return Capacity{TotalMiB: total >> 20, AvailableMiB: available >> 20}, nil
}

func (pool *ZFSPool) imagesDataset() string { return pool.name + "/images" }

func (pool *ZFSPool) baseDataset(imageReference string) string {
	return pool.imagesDataset() + "/" + imageReference
}

func (pool *ZFSPool) baseSnapshot(imageReference string) string {
	return pool.baseDataset(imageReference) + "@ready"
}

func (pool *ZFSPool) virtualMachineDataset(virtualMachineID string) string {
	return pool.name + "/vms/" + virtualMachineID
}

func (pool *ZFSPool) virtualMachineDevicePath(virtualMachineID string) string {
	return "/dev/zvol/" + pool.virtualMachineDataset(virtualMachineID)
}

func (pool *ZFSPool) snapshot(virtualMachineID, snapshotName string) string {
	return pool.virtualMachineDataset(virtualMachineID) + "@" + snapshotName
}

func (pool *ZFSPool) stagingDataset(snapshotID string) string {
	return pool.name + "/staging/" + snapshotID
}

func (pool *ZFSPool) stagingDevicePath(snapshotID string) string {
	return "/dev/zvol/" + pool.stagingDataset(snapshotID)
}
