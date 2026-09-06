package vm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const warmImageDelay = 5 * time.Minute

// WarmRuntime provides optional Firecracker warm-image operations.
type WarmRuntime interface {
	Compatibility() string
	CreateMemorySnapshot(context.Context, RuntimeMachine) (string, string, error)
}

// WarmImageStore owns host-local warm artifacts.
type WarmImageStore interface {
	FindWarmImage(context.Context, Image, MemorySnapshotConfiguration, string) (bool, error)
	PromoteWarmImage(context.Context, WarmImagePromotion) error
	RemoveOtherWarmImages(context.Context, string, string) error
}

// WarmImagePromotion contains the files and disk source for one warm image.
type WarmImagePromotion struct {
	Image                    Image
	Configuration            MemorySnapshotConfiguration
	FirecrackerCompatibility string
	SourceVirtualMachineID   string
	StateFile                string
	MemoryFile               string
}

// WarmImageBuilder creates optional host-local warm artifacts.
type WarmImageBuilder struct {
	manager     *Manager
	runtime     WarmRuntime
	store       WarmImageStore
	warmupDelay time.Duration
}

// NewWarmImageBuilder returns a warm image builder.
func NewWarmImageBuilder(manager *Manager, runtime WarmRuntime, store WarmImageStore) *WarmImageBuilder {
	return &WarmImageBuilder{manager: manager, runtime: runtime, store: store, warmupDelay: warmImageDelay}
}

// EnsureMemorySnapshot creates the requested warm image when it is absent.
func (builder *WarmImageBuilder) EnsureMemorySnapshot(ctx context.Context, image Image) error {
	configuration := image.MemorySnapshotConfiguration
	if !image.CacheImage || !image.MemorySnapshot || configuration == nil {
		return nil
	}

	compatibility := builder.runtime.Compatibility()
	key := WarmImageKey(image, *configuration, compatibility)
	if err := builder.store.RemoveOtherWarmImages(ctx, image.Name, key); err != nil {
		return err
	}
	found, err := builder.store.FindWarmImage(ctx, image, *configuration, compatibility)
	if err != nil || found {
		return err
	}

	identifier := "warm-" + key[:32]
	return builder.manager.RunTemporary(ctx, identifier, warmupSpecification(image, *configuration), func(machine RuntimeMachine) error {
		return builder.capture(ctx, machine, image, *configuration, compatibility)
	})
}

func (builder *WarmImageBuilder) capture(
	ctx context.Context,
	machine RuntimeMachine,
	image Image,
	configuration MemorySnapshotConfiguration,
	compatibility string,
) error {
	if err := waitForWarmImage(ctx, builder.warmupDelay); err != nil {
		return err
	}
	if err := builder.manager.runtime.Pause(ctx, machine); err != nil {
		return fmt.Errorf("pause warm virtual machine: %w", err)
	}
	stateFile, memoryFile, err := builder.runtime.CreateMemorySnapshot(ctx, machine)
	if err != nil {
		return err
	}
	return builder.store.PromoteWarmImage(ctx, WarmImagePromotion{
		Image: image, Configuration: configuration, FirecrackerCompatibility: compatibility,
		SourceVirtualMachineID: machine.ID, StateFile: stateFile, MemoryFile: memoryFile,
	})
}

// WarmImageKey identifies an exact image, shape, and Firecracker build.
func WarmImageKey(image Image, configuration MemorySnapshotConfiguration, compatibility string) string {
	identity := struct {
		Reference                string
		Architecture             string
		RootfsSHA256             string
		KernelSHA256             string
		Configuration            MemorySnapshotConfiguration
		FirecrackerCompatibility string
	}{
		Reference:                image.Name,
		Architecture:             image.Architecture,
		RootfsSHA256:             strings.ToLower(image.RootfsSHA256),
		KernelSHA256:             strings.ToLower(image.KernelSHA256),
		Configuration:            configuration,
		FirecrackerCompatibility: compatibility,
	}
	data, _ := json.Marshal(identity)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func warmupSpecification(image Image, configuration MemorySnapshotConfiguration) Specification {
	image.MemorySnapshot = false
	image.MemorySnapshotConfiguration = nil
	return Specification{
		VirtualCPUCount: configuration.VirtualCPUCount,
		MemoryMiB:       configuration.MemoryMiB,
		DiskMiB:         configuration.DiskMiB,
		Image:           image,
		Network:         NetworkConfiguration{Egress: EgressNone},
	}
}

func waitForWarmImage(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RunTemporary runs an operation on a manager-owned temporary virtual machine.
func (manager *Manager) RunTemporary(
	ctx context.Context,
	identifier string,
	specification Specification,
	operation func(RuntimeMachine) error,
) (operationError error) {
	if !validIdentifier(identifier) {
		return ErrConflict
	}
	manager.allocationMutex.Lock()
	if manager.temporaryIdentifiers[identifier] {
		manager.allocationMutex.Unlock()
		return ErrConflict
	}
	if _, err := manager.store.readDesired(identifier); err == nil {
		manager.allocationMutex.Unlock()
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		manager.allocationMutex.Unlock()
		return err
	}
	userID, err := manager.allocateUserID()
	if err != nil {
		manager.allocationMutex.Unlock()
		return err
	}
	manager.temporaryUserIDs[userID] = true
	manager.temporaryIdentifiers[identifier] = true
	manager.allocationMutex.Unlock()
	defer func() {
		manager.allocationMutex.Lock()
		delete(manager.temporaryUserIDs, userID)
		delete(manager.temporaryIdentifiers, identifier)
		manager.allocationMutex.Unlock()
	}()
	desired := DesiredRecord{ID: identifier, UserID: userID, GroupID: userID, Specification: cloneSpecification(specification)}
	interfaceState, err := manager.network.Ensure(ctx, networkRequest(desired))
	if err != nil {
		_ = manager.network.Release(context.WithoutCancel(ctx), NetworkReleaseRequest{
			VirtualMachineID: identifier, UserID: userID,
			WireGuardMeshIPv6: specification.Network.WireGuardMeshIPv6,
		})
		return fmt.Errorf("ensure temporary VM network: %w", err)
	}
	machine := runtimeMachine(desired, interfaceState)
	defer func() {
		cleanupContext := context.WithoutCancel(ctx)
		operationError = errors.Join(
			operationError,
			manager.runtime.Remove(cleanupContext, machine),
			manager.network.Release(cleanupContext, NetworkReleaseRequest{
				VirtualMachineID: identifier, UserID: userID,
				WireGuardMeshIPv6: specification.Network.WireGuardMeshIPv6,
			}),
			manager.storage.Release(cleanupContext, identifier),
		)
	}()
	if err := manager.runtime.Start(ctx, machine); err != nil {
		return fmt.Errorf("start temporary VM: %w", err)
	}
	return operation(machine)
}
