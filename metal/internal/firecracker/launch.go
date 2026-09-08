package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

// prepareBoot starts a jailed Firecracker process and configures it for a cold
// boot: machine shape, kernel, drives, network, and the metadata service.
func (runtime *Runtime) prepareBoot(ctx context.Context, configuration vm.RuntimeMachine) error {
	if err := runtime.prepareLaunch(ctx, configuration); err != nil {
		return err
	}

	bootConfiguration, err := runtime.virtualMachineStorage.PrepareBoot(ctx, storage.VirtualMachineStorageRequest{
		VirtualMachineID: configuration.ID,
		ImageReference:   configuration.Specification.Image.Name,
		Image:            configuration.Specification.Image,
		ChrootRoot:       runtime.configuration.chrootRoot(configuration.ID),
		UserID:           configuration.UserID,
		GroupID:          configuration.GroupID,
		DiskMiB:          configuration.Specification.DiskMiB,
	})
	if err != nil {
		return err
	}
	runtime.logger.Debug("configured Firecracker VM", "virtual_machine_id", configuration.ID, "kernel", bootConfiguration.Kernel, "cmdline", bootArguments(bootConfiguration, configuration.NetworkInterface))
	return configure(
		ctx,
		api.New(runtime.configuration.socketPath(configuration.ID)),
		configuration.ID,
		configuration.Specification,
		bootConfiguration,
		configuration.NetworkInterface,
	)
}

// prepareLaunch puts the jail in place and starts the unit. The console PTY is
// opened before the unit starts, so no guest output is lost.
func (runtime *Runtime) prepareLaunch(ctx context.Context, configuration vm.RuntimeMachine) error {
	if err := runtime.configuration.writeJailerEnv(
		configuration.ID,
		runtime.configuration.jailerArgs(
			configuration.ID,
			configuration.UserID,
			configuration.GroupID,
			configuration.NetworkInterface.NetworkNamespacePath,
		),
	); err != nil {
		return err
	}
	if err := runtime.configuration.linkSocket(configuration.ID); err != nil {
		return err
	}
	// Open the PTY before systemd starts the unit.
	if err := runtime.serialBroker.Open(configuration.ID); err != nil {
		return err
	}
	if err := runtime.units.Start(ctx, configuration.ID); err != nil {
		return err
	}
	// The unit holds the slave now, so systemd can keep the master.
	if err := runtime.serialBroker.Persist(configuration.ID); err != nil {
		runtime.logger.Warn(
			"console master not preserved; a metald exit stops this VM",
			"virtual_machine_id", configuration.ID,
			"error", err,
		)
	}
	if err := runtime.units.SetLimits(ctx, configuration.ID, resourceLimits(configuration.Specification)); err != nil {
		return err
	}
	if err := waitSocket(ctx, runtime.configuration.socketPath(configuration.ID)); err != nil {
		return err
	}

	return nil
}

// relaunch discards the previous jail and boots from a clean one. A jail is
// never reused, because leftover state is harder to reason about than a rebuild.
func (runtime *Runtime) relaunch(ctx context.Context, configuration vm.RuntimeMachine) error {
	_ = runtime.units.Stop(ctx, configuration.ID)
	_ = os.RemoveAll(filepath.Dir(runtime.configuration.chrootRoot(configuration.ID)))
	return runtime.prepareBoot(ctx, configuration)
}

// launchSnapshot restores a guest from a memory snapshot instead of booting it.
// The guest is resumed only after the metadata is replaced, so it never reads the
// values of the VM the snapshot came from. When resume is false the guest is left
// paused, so a caller can restore state without running the vCPUs.
func (runtime *Runtime) launchSnapshot(
	ctx context.Context,
	configuration vm.RuntimeMachine,
	rootSnapshot string,
	stateFile string,
	memoryFile string,
	metadata map[string]any,
	resume bool,
) error {
	_ = runtime.units.Stop(ctx, configuration.ID)
	_ = os.RemoveAll(filepath.Dir(runtime.configuration.chrootRoot(configuration.ID)))

	if err := runtime.prepareLaunch(ctx, configuration); err != nil {
		return err
	}

	if err := runtime.virtualMachineStorage.PrepareRootFileSystem(ctx, storage.VirtualMachineStorageRequest{
		VirtualMachineID: configuration.ID,
		ImageReference:   configuration.Specification.Image.Name,
		Image:            configuration.Specification.Image,
		ChrootRoot:       runtime.configuration.chrootRoot(configuration.ID),
		UserID:           configuration.UserID,
		GroupID:          configuration.GroupID,
		DiskMiB:          configuration.Specification.DiskMiB,
		SourceSnapshot:   rootSnapshot,
	}); err != nil {
		return err
	}

	stage := filepath.Join(runtime.configuration.chrootRoot(configuration.ID), "snap")
	if err := mkdirChown(stage, configuration.UserID, configuration.GroupID); err != nil {
		return err
	}
	if err := copyChown(
		ctx,
		stateFile,
		filepath.Join(stage, "state"),
		configuration.UserID,
		configuration.GroupID,
	); err != nil {
		return err
	}
	if err := copyChown(
		ctx,
		memoryFile,
		filepath.Join(stage, "mem"),
		configuration.UserID,
		configuration.GroupID,
	); err != nil {
		return err
	}

	client := api.New(runtime.configuration.socketPath(configuration.ID))
	if err := client.LoadSnapshot(ctx, api.LoadSnapshotRequest{
		SnapshotPath: "snap/state",
		Memory:       api.MemoryBackend{Path: "snap/mem", Type: "File"},
		Resume:       false,
	}); err != nil {
		return err
	}
	if metadata != nil {
		if err := client.PutMMDS(ctx, metadata); err != nil {
			runtime.logger.Error("MMDS refresh failed", "virtual_machine_id", configuration.ID, "error", err)
		}
	}
	if !resume {
		return nil
	}

	return client.Resume(ctx)
}

// launchWarmImage restores the warm image of this exact image and VM shape.
// A missing or mismatched artifact reports ErrNotFound, which asks for a cold boot.
func (runtime *Runtime) launchWarmImage(ctx context.Context, configuration vm.RuntimeMachine, imageReference string) error {
	memorySnapshotConfiguration := configuration.Specification.Image.MemorySnapshotConfiguration
	if memorySnapshotConfiguration == nil || configuration.Specification.Image.Name != imageReference {
		return vm.ErrNotFound
	}

	artifacts, found, err := runtime.imageStore.WarmImage(
		ctx,
		configuration.Specification.Image,
		*memorySnapshotConfiguration,
		runtime.firecrackerCompatibility(),
	)
	if err != nil {
		return err
	}
	if !found {
		return vm.ErrNotFound
	}

	metadata := metadataServiceData(
		configuration.ID, configuration.NetworkInterface.GuestIPAddress, configuration.NetworkInterface.MACAddress, configuration.Specification,
	)
	return runtime.launchSnapshot(
		ctx,
		configuration,
		artifacts.RootSnapshot,
		artifacts.StateFile,
		artifacts.MemoryFile,
		metadata,
		true,
	)
}

// firecrackerCompatibility identifies the Firecracker build. A memory snapshot
// restores only into the build that wrote it, so the size and modification time
// stand in for a version the binary does not report.
func (runtime *Runtime) firecrackerCompatibility() string {
	information, err := os.Stat(runtime.configuration.FirecrackerBin)
	if err != nil {
		return runtime.configuration.FirecrackerBin
	}
	return fmt.Sprintf(
		"%s:%d:%d",
		runtime.configuration.FirecrackerBin,
		information.Size(),
		information.ModTime().UnixNano(),
	)
}

// hasMatchingMemorySnapshot reports whether a warm image could exist for this
// VM. The shape must match exactly, because a snapshot fixes CPU, memory, and disk.
func (runtime *Runtime) hasMatchingMemorySnapshot(specification vm.Specification) bool {
	configuration := specification.Image.MemorySnapshotConfiguration
	return specification.Image.CacheImage &&
		specification.Image.MemorySnapshot &&
		configuration != nil &&
		configuration.VirtualCPUCount == specification.VirtualCPUCount &&
		configuration.MemoryMiB == specification.MemoryMiB &&
		configuration.DiskMiB == specification.DiskMiB
}

// mkdirChown creates a directory the jailed process can write to.
func mkdirChown(path string, userID, groupID uint32) error {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	return os.Chown(path, int(userID), int(groupID))
}

// copyChown places a file inside the jail and gives it to the VM user.
func copyChown(
	ctx context.Context,
	source string,
	destination string,
	userID uint32,
	groupID uint32,
) error {
	if err := storage.LinkOrCopy(ctx, source, destination); err != nil {
		return err
	}
	return os.Chown(destination, int(userID), int(groupID))
}
