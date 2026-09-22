// Package host synchronizes controller-owned host state and reports capacity.
package host

import (
	"context"
	"fmt"
	"runtime"

	"github.com/frappe/atlas/metal/internal/network"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
	"github.com/frappe/atlas/metal/internal/vm/migration"
)

// DesiredState contains the complete controller-owned host sets.
type DesiredState struct {
	WireGuardPeers                    []network.WireGuardPeer
	Images                            []vm.Image
	PrivilegedVirtualMachineAddresses []string
	// UnicastEnabled selects the unicast NDP transport.
	UnicastEnabled bool
}

// SyncResult contains what the controller reads back from one sync exchange.
type SyncResult struct {
	Capacity             Capacity
	VirtualMachineStates map[string]vm.State
	// UplinkMAC is the discovery uplink MAC of this host.
	UplinkMAC string
}

// Capacity contains current host compute and storage capacity.
type Capacity struct {
	TotalCPUMillicores     int
	AvailableCPUMillicores int
	VirtualMachineCount    int
	TotalMemoryMiB         int
	AvailableMemoryMiB     int
	TotalStorageMiB        int
	AvailableStorageMiB    int
}

// PrivilegedMesh replaces the privileged virtual machine address set.
type PrivilegedMesh interface {
	ApplyPrivilegedAddresses(context.Context, []string) error
}

// WireGuardManager replaces controller-owned WireGuard peers.
type WireGuardManager interface {
	Apply(context.Context, []network.WireGuardPeer) error
}

// UnicastTransport switches the unicast NDP transport on and off.
type UnicastTransport interface {
	Enable(context.Context) error
	Disable(context.Context) error
}

// PeerStateSyncer reloads the mesh peer state after the WireGuard peers change.
type PeerStateSyncer interface {
	SyncPeerState(context.Context) error
}

// UplinkReporter reports the discovery uplink MAC of this host.
type UplinkReporter interface {
	UplinkMAC() (string, error)
}

// ImagePolicyStore replaces controller-owned image policies.
type ImagePolicyStore interface {
	SetImagePolicies(context.Context, []vm.Image) error
}

// VirtualMachineSource supplies valid host reservations.
type VirtualMachineSource interface {
	List(context.Context) ([]vm.Information, error)
}

// StorageCapacitySource supplies current pool capacity.
type StorageCapacitySource interface {
	Capacity(context.Context) (storage.Capacity, error)
}

// MigrationReservations returns capacity held by incoming migration destinations.
// Destinations are absent from the VM list until reconstructed.
type MigrationReservations func(context.Context) ([]migration.DestinationReservation, error)

// Dependencies contains host synchronization services.
type Dependencies struct {
	Mesh                  PrivilegedMesh
	WireGuard             WireGuardManager
	Unicast               UnicastTransport
	PeerState             PeerStateSyncer
	Uplink                UplinkReporter
	Images                ImagePolicyStore
	VirtualMachines       VirtualMachineSource
	Storage               StorageCapacitySource
	MigrationReservations MigrationReservations
	// Wake starts a reconcile pass, so new controller state is applied at once
	// instead of at the next tick.
	Wake func()
}

// Service owns controller synchronization and host capacity calculation.
type Service struct {
	mesh                  PrivilegedMesh
	wireGuard             WireGuardManager
	unicast               UnicastTransport
	peerState             PeerStateSyncer
	uplink                UplinkReporter
	images                ImagePolicyStore
	virtualMachines       VirtualMachineSource
	storage               StorageCapacitySource
	migrationReservations MigrationReservations
	wake                  func()
}

// NewService returns a host service with explicit dependencies.
func NewService(dependencies Dependencies) (*Service, error) {
	if dependencies.WireGuard == nil || dependencies.Images == nil ||
		dependencies.VirtualMachines == nil || dependencies.Storage == nil || dependencies.Wake == nil {
		return nil, fmt.Errorf("host service dependencies are required")
	}

	return &Service{
		mesh:                  dependencies.Mesh,
		wireGuard:             dependencies.WireGuard,
		unicast:               dependencies.Unicast,
		peerState:             dependencies.PeerState,
		uplink:                dependencies.Uplink,
		images:                dependencies.Images,
		virtualMachines:       dependencies.VirtualMachines,
		storage:               dependencies.Storage,
		migrationReservations: dependencies.MigrationReservations,
		wake:                  dependencies.Wake,
	}, nil
}

// Synchronize applies controller-owned state and returns the sync result. Each
// set is replaced in full, so the controller never sends incremental changes.
func (service *Service) Synchronize(ctx context.Context, desired DesiredState) (SyncResult, error) {
	addresses := desired.PrivilegedVirtualMachineAddresses
	if service.mesh != nil {
		if err := service.mesh.ApplyPrivilegedAddresses(ctx, addresses); err != nil {
			return SyncResult{}, fmt.Errorf("apply privileged virtual machine addresses: %w", err)
		}
	}
	if err := service.wireGuard.Apply(ctx, desired.WireGuardPeers); err != nil {
		return SyncResult{}, fmt.Errorf("apply WireGuard peers: %w", err)
	}
	if service.peerState != nil {
		if err := service.peerState.SyncPeerState(ctx); err != nil {
			return SyncResult{}, fmt.Errorf("sync mesh peer state: %w", err)
		}
	}
	if err := service.applyUnicast(ctx, desired.UnicastEnabled); err != nil {
		return SyncResult{}, err
	}
	if err := service.images.SetImagePolicies(ctx, desired.Images); err != nil {
		return SyncResult{}, fmt.Errorf("apply image policies: %w", err)
	}

	service.wake()

	virtualMachines, err := service.virtualMachines.List(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("list virtual machine reservations: %w", err)
	}

	capacity, err := service.capacityOf(ctx, virtualMachines)
	if err != nil {
		return SyncResult{}, err
	}

	states := make(map[string]vm.State, len(virtualMachines))
	for _, information := range virtualMachines {
		states[information.ID] = information.State
	}

	result := SyncResult{Capacity: capacity, VirtualMachineStates: states}
	if service.uplink != nil {
		uplinkMAC, err := service.uplink.UplinkMAC()
		if err != nil {
			return SyncResult{}, fmt.Errorf("read uplink MAC: %w", err)
		}
		result.UplinkMAC = uplinkMAC
	}

	return result, nil
}

// applyUnicast switches the unicast NDP transport.
func (service *Service) applyUnicast(ctx context.Context, enabled bool) error {
	if service.unicast == nil {
		if enabled {
			return fmt.Errorf("enable unicast transport: Atlas WG Mesh is disabled")
		}
		return nil
	}

	if enabled {
		if err := service.unicast.Enable(ctx); err != nil {
			return fmt.Errorf("enable unicast transport: %w", err)
		}
		return nil
	}
	if err := service.unicast.Disable(ctx); err != nil {
		return fmt.Errorf("disable unicast transport: %w", err)
	}
	return nil
}

// Capacity returns current host capacity and valid VM reservations. CPU is
// reported against reservations, while memory and storage are read from the host.
func (service *Service) Capacity(ctx context.Context) (Capacity, error) {
	virtualMachines, err := service.virtualMachines.List(ctx)
	if err != nil {
		return Capacity{}, fmt.Errorf("list virtual machine reservations: %w", err)
	}

	return service.capacityOf(ctx, virtualMachines)
}

// capacityOf reports capacity against reservations the caller already listed.
// Incoming destinations reserve capacity before their data arrives.
func (service *Service) capacityOf(ctx context.Context, virtualMachines []vm.Information) (Capacity, error) {
	reservedCPUMillicores := 0
	for _, information := range virtualMachines {
		reservedCPUMillicores += information.CPUMillicores
	}

	reservedMemoryMiB, reservedStorageMiB := 0, 0
	if service.migrationReservations != nil {
		reservations, err := service.migrationReservations(ctx)
		if err != nil {
			return Capacity{}, fmt.Errorf("list migration reservations: %w", err)
		}
		for _, reservation := range reservations {
			reservedCPUMillicores += reservation.CPUMillicores
			reservedMemoryMiB += reservation.MemoryMiB
			reservedStorageMiB += reservation.DiskMiB
		}
	}

	totalMemoryMiB, availableMemoryMiB, err := memoryCapacityMiB()
	if err != nil {
		return Capacity{}, err
	}

	storageCapacity, err := service.storage.Capacity(ctx)
	if err != nil {
		return Capacity{}, fmt.Errorf("read storage capacity: %w", err)
	}

	totalCPUMillicores := runtime.NumCPU() * 1000

	return Capacity{
		TotalCPUMillicores:     totalCPUMillicores,
		AvailableCPUMillicores: max(totalCPUMillicores-reservedCPUMillicores, 0),
		VirtualMachineCount:    len(virtualMachines),
		TotalMemoryMiB:         totalMemoryMiB,
		AvailableMemoryMiB:     max(availableMemoryMiB-reservedMemoryMiB, 0),
		TotalStorageMiB:        int(storageCapacity.TotalMiB),
		AvailableStorageMiB:    max(int(storageCapacity.AvailableMiB)-reservedStorageMiB, 0),
	}, nil
}
