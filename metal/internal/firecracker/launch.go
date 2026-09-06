// Package firecracker controls Firecracker virtual machines.
package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

type virtualMachineStorage interface {
	PrepareBoot(ctx context.Context, request storage.VirtualMachineStorageRequest) (storage.BootConfiguration, error)
	PrepareRootFileSystem(ctx context.Context, request storage.VirtualMachineStorageRequest) error
	Release(ctx context.Context, virtualMachineID string) error
}

type imageStore interface {
	EnsureImage(ctx context.Context, image vm.Image) error
	WarmImage(
		ctx context.Context,
		image vm.Image,
		configuration vm.MemorySnapshotConfiguration,
		firecrackerCompatibility string,
	) (storage.WarmImageArtifacts, bool, error)
	RecordImageUse(imageReference string, usedAt time.Time) error
}

// consoleBroker manages each VM's serial console PTY.
type consoleBroker interface {
	Open(id string) error
	Close(id string) error
}

// Runtime manages Firecracker virtual machines on one host.
type Runtime struct {
	configuration         Config
	units                 platform.Manager
	virtualMachineStorage virtualMachineStorage
	imageStore            imageStore
	consoleBroker         consoleBroker
	sshSlots              chan struct{}
	logger                *slog.Logger
}

// maxConcurrentSSHSessions limits host-wide SSH console sessions.
const maxConcurrentSSHSessions = 32

// NewRuntime returns a Firecracker runtime.
func NewRuntime(
	configuration Config,
	units platform.Manager,
	virtualMachineStorage virtualMachineStorage,
	imageStore imageStore,
	consoleBroker consoleBroker,
	logger *slog.Logger,
) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		configuration:         configuration,
		units:                 units,
		virtualMachineStorage: virtualMachineStorage,
		imageStore:            imageStore,
		consoleBroker:         consoleBroker,
		sshSlots:              make(chan struct{}, maxConcurrentSSHSessions),
		logger:                logger,
	}
}

func (d *Runtime) prepareBoot(ctx context.Context, configuration vm.RuntimeMachine) error {
	if err := d.prepareLaunch(ctx, configuration); err != nil {
		return err
	}

	bootConfiguration, err := d.virtualMachineStorage.PrepareBoot(ctx, storage.VirtualMachineStorageRequest{
		VirtualMachineID: configuration.ID,
		ImageReference:   configuration.Specification.Image.Name,
		Image:            configuration.Specification.Image,
		ChrootRoot:       d.configuration.chrootRoot(configuration.ID),
		UserID:           configuration.UserID,
		GroupID:          configuration.GroupID,
		DiskMiB:          configuration.Specification.DiskMiB,
	})
	if err != nil {
		return err
	}
	d.logger.Debug("configured Firecracker VM", "virtual_machine_id", configuration.ID, "kernel", bootConfiguration.Kernel, "cmdline", bootArguments(bootConfiguration, configuration.NetworkInterface))
	return configure(
		ctx,
		api.New(d.configuration.sockPath(configuration.ID)),
		configuration.ID,
		configuration.Specification,
		bootConfiguration,
		configuration.NetworkInterface,
	)
}

func (d *Runtime) prepareLaunch(ctx context.Context, configuration vm.RuntimeMachine) error {
	if err := d.configuration.writeJailerEnv(
		configuration.ID,
		d.configuration.jailerArgs(
			configuration.ID,
			configuration.UserID,
			configuration.GroupID,
			configuration.NetworkInterface.NetworkNamespacePath,
		),
	); err != nil {
		return err
	}
	if err := d.configuration.linkSocket(configuration.ID); err != nil {
		return err
	}
	// Open the PTY before systemd starts the unit.
	if err := d.consoleBroker.Open(configuration.ID); err != nil {
		return err
	}
	if err := d.units.Start(ctx, configuration.ID); err != nil {
		return err
	}
	if err := d.units.SetLimits(ctx, configuration.ID, resourceLimits(configuration.Specification)); err != nil {
		return err
	}
	if err := waitSocket(ctx, d.configuration.sockPath(configuration.ID)); err != nil {
		return err
	}

	return nil
}

func (d *Runtime) relaunch(ctx context.Context, configuration vm.RuntimeMachine) error {
	_ = d.units.Stop(ctx, configuration.ID)
	_ = os.RemoveAll(filepath.Dir(d.configuration.chrootRoot(configuration.ID)))
	return d.prepareBoot(ctx, configuration)
}

func (d *Runtime) launchSnapshot(
	ctx context.Context,
	configuration vm.RuntimeMachine,
	rootSnapshot string,
	stateFile string,
	memoryFile string,
	metadata map[string]any,
) error {
	_ = d.units.Stop(ctx, configuration.ID)
	_ = os.RemoveAll(filepath.Dir(d.configuration.chrootRoot(configuration.ID)))

	if err := d.prepareLaunch(ctx, configuration); err != nil {
		return err
	}

	if err := d.virtualMachineStorage.PrepareRootFileSystem(ctx, storage.VirtualMachineStorageRequest{
		VirtualMachineID: configuration.ID,
		ImageReference:   configuration.Specification.Image.Name,
		Image:            configuration.Specification.Image,
		ChrootRoot:       d.configuration.chrootRoot(configuration.ID),
		UserID:           configuration.UserID,
		GroupID:          configuration.GroupID,
		DiskMiB:          configuration.Specification.DiskMiB,
		SourceSnapshot:   rootSnapshot,
	}); err != nil {
		return err
	}

	stage := filepath.Join(d.configuration.chrootRoot(configuration.ID), "snap")
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

	client := api.New(d.configuration.sockPath(configuration.ID))
	if err := client.LoadSnapshot(ctx, api.LoadSnapshotRequest{
		SnapshotPath: "snap/state",
		Memory:       api.MemoryBackend{Path: "snap/mem", Type: "File"},
		Resume:       false,
	}); err != nil {
		return err
	}
	if metadata != nil {
		if err := client.PutMMDS(ctx, metadata); err != nil {
			d.logger.Error("MMDS refresh failed", "virtual_machine_id", configuration.ID, "error", err)
		}
	}
	return client.Resume(ctx)
}

func (d *Runtime) launchWarmImage(ctx context.Context, configuration vm.RuntimeMachine, ref string) error {
	memorySnapshotConfiguration := configuration.Specification.Image.MemorySnapshotConfiguration
	if memorySnapshotConfiguration == nil || configuration.Specification.Image.Name != ref {
		return vm.ErrNotFound
	}

	artifacts, found, err := d.imageStore.WarmImage(
		ctx,
		configuration.Specification.Image,
		*memorySnapshotConfiguration,
		d.firecrackerCompatibility(),
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
	return d.launchSnapshot(
		ctx,
		configuration,
		artifacts.RootSnapshot,
		artifacts.StateFile,
		artifacts.MemoryFile,
		metadata,
	)
}

func (d *Runtime) firecrackerCompatibility() string {
	information, err := os.Stat(d.configuration.FirecrackerBin)
	if err != nil {
		return d.configuration.FirecrackerBin
	}
	return fmt.Sprintf(
		"%s:%d:%d",
		d.configuration.FirecrackerBin,
		information.Size(),
		information.ModTime().UnixNano(),
	)
}

func (d *Runtime) hasMatchingMemorySnapshot(spec vm.Specification) bool {
	configuration := spec.Image.MemorySnapshotConfiguration
	return spec.Image.CacheImage &&
		spec.Image.MemorySnapshot &&
		configuration != nil &&
		configuration.VirtualCPUCount == spec.VirtualCPUCount &&
		configuration.MemoryMiB == spec.MemoryMiB &&
		configuration.DiskMiB == spec.DiskMiB
}

func mkdirChown(path string, userID, groupID uint32) error {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	return os.Chown(path, int(userID), int(groupID))
}

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
