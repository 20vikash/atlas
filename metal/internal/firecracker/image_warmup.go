package firecracker

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

type temporaryMachineRunner interface {
	RunTemporary(context.Context, string, vm.Spec, func(vm.RuntimeMachine) error) error
}

// MemorySnapshotBuilder creates local warm image artifacts.
type MemorySnapshotBuilder struct {
	machines    temporaryMachineRunner
	runtime     *Runtime
	images      imageStore
	warmupDelay time.Duration
}

// NewMemorySnapshotBuilder returns a warm image builder.
func NewMemorySnapshotBuilder(machines temporaryMachineRunner, runtime *Runtime, images imageStore) *MemorySnapshotBuilder {
	return &MemorySnapshotBuilder{
		machines: machines, runtime: runtime, images: images,
		warmupDelay: 5 * time.Minute,
	}
}

// EnsureMemorySnapshot builds the requested local memory snapshot when needed.
func (builder *MemorySnapshotBuilder) EnsureMemorySnapshot(ctx context.Context, image vm.ImageRef) error {
	configuration := image.MemorySnapshotConfiguration
	if !image.CacheImage || !image.MemorySnapshot || configuration == nil {
		return nil
	}
	if err := builder.images.EnsureImage(ctx, image); err != nil {
		return err
	}
	compatibility := builder.runtime.firecrackerCompatibility()
	key := storage.WarmImageKey(image, *configuration, compatibility)
	if err := builder.images.RemoveOtherWarmImages(ctx, image.Name, key); err != nil {
		return err
	}
	if _, found, err := builder.images.WarmImage(ctx, image, *configuration, compatibility); err != nil || found {
		return err
	}
	return builder.build(ctx, image, *configuration, compatibility, key)
}

func (builder *MemorySnapshotBuilder) build(
	ctx context.Context,
	image vm.ImageRef,
	configuration vm.MemorySnapshotConfiguration,
	compatibility string,
	key string,
) error {
	identifier := "warm-" + key[:32]
	return builder.machines.RunTemporary(ctx, identifier, warmupSpecification(image, configuration), func(machine vm.RuntimeMachine) error {
		if err := waitForWarmup(ctx, builder.warmupDelay); err != nil {
			return err
		}
		if err := builder.runtime.Pause(ctx, machine); err != nil {
			return fmt.Errorf("pause warmup virtual machine: %w", err)
		}
		return builder.capture(ctx, machine, image, configuration, compatibility)
	})
}

func (builder *MemorySnapshotBuilder) capture(
	ctx context.Context,
	machine vm.RuntimeMachine,
	image vm.ImageRef,
	configuration vm.MemorySnapshotConfiguration,
	compatibility string,
) error {
	const snapshotName = "warm"
	if err := builder.images.CreateWarmSourceSnapshot(ctx, machine.ID, snapshotName); err != nil {
		return fmt.Errorf("snapshot warmup disk: %w", err)
	}
	defer builder.images.DeleteWarmSourceSnapshot(context.WithoutCancel(ctx), machine.ID, snapshotName)
	stateFile, memoryFile, err := builder.runtime.CreateMemorySnapshot(ctx, machine)
	if err != nil {
		return err
	}
	_, err = builder.images.PromoteWarmSnapshot(ctx, storage.WarmImagePromotion{
		Image: image, Configuration: configuration, FirecrackerCompatibility: compatibility,
		SourceVirtualMachineID: machine.ID, SourceSnapshotName: snapshotName,
		StateFile: stateFile, MemoryFile: memoryFile,
	})
	if err != nil {
		return fmt.Errorf("store warm image: %w", err)
	}
	return nil
}

// CreateMemorySnapshot writes Firecracker state and memory files.
func (runtime *Runtime) CreateMemorySnapshot(ctx context.Context, machine vm.RuntimeMachine) (string, string, error) {
	directory := filepath.Join(runtime.configuration.chrootRoot(machine.ID), "warm")
	if err := mkdirChown(directory, machine.UserID, machine.GroupID); err != nil {
		return "", "", err
	}
	if err := api.New(runtime.configuration.sockPath(machine.ID)).CreateSnapshot(ctx, api.CreateSnapshotRequest{
		SnapshotType: "Full", SnapshotPath: "warm/state", MemoryFile: "warm/memory",
	}); err != nil {
		return "", "", fmt.Errorf("create warmup memory snapshot: %w", err)
	}
	return filepath.Join(directory, "state"), filepath.Join(directory, "memory"), nil
}

func warmupSpecification(image vm.ImageRef, configuration vm.MemorySnapshotConfiguration) vm.Spec {
	image.MemorySnapshot = false
	image.MemorySnapshotConfiguration = nil
	return vm.Spec{
		VCPUs: configuration.VirtualCPUCount, MemoryMiB: configuration.MemoryMiB,
		DiskMiB: configuration.DiskMiB, Image: image,
		Network: vm.NetworkConfiguration{Egress: vm.EgressNone},
	}
}

func waitForWarmup(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
