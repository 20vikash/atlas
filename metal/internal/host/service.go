// Package host synchronizes controller-owned host state and reports capacity.
package host

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/frappe/atlas/metal/internal/network"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

// DesiredState contains the complete controller-owned host sets.
type DesiredState struct {
	WireGuardPeers                    []network.WireGuardPeer
	Images                            []vm.Image
	PrivilegedVirtualMachineAddresses []string
}

// Capacity contains current host compute and storage capacity.
type Capacity struct {
	TotalCPUCount       int
	AvailableCPUCount   int
	VirtualMachineCount int
	TotalMemoryMiB      int
	AvailableMemoryMiB  int
	TotalStorageMiB     int
	AvailableStorageMiB int
}

// PrivilegedMesh replaces the privileged virtual machine address set.
type PrivilegedMesh interface {
	ApplyPrivilegedAddresses(context.Context, []string) error
}

// WireGuardManager replaces controller-owned WireGuard peers.
type WireGuardManager interface {
	Apply(context.Context, []network.WireGuardPeer) error
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

// Dependencies contains host synchronization services.
type Dependencies struct {
	Mesh            PrivilegedMesh
	WireGuard       WireGuardManager
	Images          ImagePolicyStore
	VirtualMachines VirtualMachineSource
	Storage         StorageCapacitySource
	Wake            func()
}

// Service owns controller synchronization and host capacity calculation.
type Service struct {
	mesh            PrivilegedMesh
	wireGuard       WireGuardManager
	images          ImagePolicyStore
	virtualMachines VirtualMachineSource
	storage         StorageCapacitySource
	wake            func()
}

// NewService returns a host service with explicit dependencies.
func NewService(dependencies Dependencies) (*Service, error) {
	if dependencies.Mesh == nil || dependencies.WireGuard == nil || dependencies.Images == nil ||
		dependencies.VirtualMachines == nil || dependencies.Storage == nil || dependencies.Wake == nil {
		return nil, fmt.Errorf("host service dependencies are required")
	}
	return &Service{
		mesh: dependencies.Mesh, wireGuard: dependencies.WireGuard, images: dependencies.Images,
		virtualMachines: dependencies.VirtualMachines, storage: dependencies.Storage, wake: dependencies.Wake,
	}, nil
}

// Synchronize applies controller-owned state and returns current capacity.
func (service *Service) Synchronize(ctx context.Context, desired DesiredState) (Capacity, error) {
	if err := service.mesh.ApplyPrivilegedAddresses(ctx, desired.PrivilegedVirtualMachineAddresses); err != nil {
		return Capacity{}, fmt.Errorf("apply privileged virtual machine addresses: %w", err)
	}
	if err := service.wireGuard.Apply(ctx, desired.WireGuardPeers); err != nil {
		return Capacity{}, fmt.Errorf("apply WireGuard peers: %w", err)
	}
	if err := service.images.SetImagePolicies(ctx, desired.Images); err != nil {
		return Capacity{}, fmt.Errorf("apply image policies: %w", err)
	}
	service.wake()
	return service.Capacity(ctx)
}

// Capacity returns current host capacity and valid VM reservations.
func (service *Service) Capacity(ctx context.Context) (Capacity, error) {
	virtualMachines, err := service.virtualMachines.List(ctx)
	if err != nil {
		return Capacity{}, fmt.Errorf("list virtual machine reservations: %w", err)
	}

	allocatedCPUCount := 0
	for _, information := range virtualMachines {
		allocatedCPUCount += information.VirtualCPUCount
	}
	availableMemoryMiB, totalMemoryMiB, err := memoryCapacityMiB()
	if err != nil {
		return Capacity{}, err
	}
	storageCapacity, err := service.storage.Capacity(ctx)
	if err != nil {
		return Capacity{}, fmt.Errorf("read storage capacity: %w", err)
	}

	return Capacity{
		TotalCPUCount: runtime.NumCPU(), AvailableCPUCount: max(runtime.NumCPU()-allocatedCPUCount, 0),
		VirtualMachineCount: len(virtualMachines), TotalMemoryMiB: totalMemoryMiB,
		AvailableMemoryMiB: availableMemoryMiB, TotalStorageMiB: int(storageCapacity.TotalMiB),
		AvailableStorageMiB: int(storageCapacity.AvailableMiB),
	}, nil
}

func memoryCapacityMiB() (int, int, error) {
	var information syscall.Sysinfo_t
	if err := syscall.Sysinfo(&information); err != nil {
		return 0, 0, fmt.Errorf("read total memory: %w", err)
	}
	unit := uint64(information.Unit)
	if unit == 0 {
		unit = 1
	}
	available, err := availableMemoryMiB()
	return available, int((uint64(information.Totalram) * unit) >> 20), err
}

func availableMemoryMiB() (int, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || fields[0] != "MemAvailable:" || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse available memory: %w", err)
		}
		return int(value / 1024), nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	return 0, fmt.Errorf("MemAvailable is missing from /proc/meminfo")
}
