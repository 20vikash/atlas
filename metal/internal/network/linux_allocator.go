package network

import (
	"context"
	"errors"
	"fmt"

	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

const (
	tapName             = "tap0"
	gatewayIPAddress    = "172.16.0.1"
	guestIPAddress      = "172.16.0.2"
	networkPrefixLength = 24
	guestMACAddress     = "06:00:ac:10:00:02"
)

type meshRegistrar interface {
	Add(ctx context.Context, address, interfaceName string) error
	Remove(ctx context.Context, address, interfaceName string) error
}

// LinuxAllocator creates Linux network resources for virtual machines.
type LinuxAllocator struct {
	mesh meshRegistrar
}

// NewLinuxAllocator returns a Linux network allocator.
func NewLinuxAllocator(mesh *Mesh) *LinuxAllocator { return &LinuxAllocator{mesh: mesh} }

// Ensure converges all host network resources to the requested state.
func (allocator *LinuxAllocator) Ensure(ctx context.Context, desired vm.NetworkRequest) (vm.NetworkInterface, error) {
	request := request{
		VirtualMachineID:              desired.VirtualMachineID,
		Egress:                        desired.Configuration.Egress,
		PublicIPv4:                    desired.Configuration.PublicIPv4,
		WireGuardMeshIPv6:             desired.Configuration.WireGuardMeshIPv6,
		PrivateNetworkThroughputMiBps: desired.Configuration.PrivateNetworkThroughputMiBps,
		PublicNetworkThroughputMiBps:  desired.Configuration.PublicNetworkThroughputMiBps,
		UserID:                        desired.UserID,
		GroupID:                       desired.GroupID,
	}
	exists, err := networkNamespaceExists(ctx, request.VirtualMachineID)
	if err != nil {
		return vm.NetworkInterface{}, err
	}
	if !exists {
		return allocator.allocate(ctx, request)
	}
	if err := allocator.ensureExisting(ctx, request); err != nil {
		return vm.NetworkInterface{}, err
	}
	return allocator.resolve(request.VirtualMachineID), nil
}

func (allocator *LinuxAllocator) ensureExisting(ctx context.Context, request request) error {
	if request.PublicIPv4 != "" && !request.Egress.HasInternetPath() {
		return fmt.Errorf("public IPv4 requires %s egress", vm.EgressUplink)
	}
	if err := ensureNamespaceBase(ctx, request); err != nil {
		return err
	}
	if request.PublicIPv4 == "" {
		if err := removePublicIPv4Rules(ctx, request.VirtualMachineID); err != nil {
			return err
		}
		if err := removePublicIPv4NamespaceRules(ctx, request.VirtualMachineID); err != nil {
			return err
		}
	}
	if !request.Egress.HasVirtualEthernet() {
		if err := allocator.removeMeshRegistration(ctx, request.UserID, request.WireGuardMeshIPv6); err != nil {
			return err
		}
	}
	if !request.Egress.HasInternetPath() {
		if err := removeInternetPath(ctx, request.VirtualMachineID, request.UserID); err != nil {
			return err
		}
	}
	if err := setVirtualEthernet(ctx, request.VirtualMachineID, request.UserID, request.Egress.HasVirtualEthernet()); err != nil {
		return err
	}
	if request.Egress.HasInternetPath() {
		if err := addInternetPath(ctx, request.VirtualMachineID, request.UserID); err != nil {
			return err
		}
	}
	if request.PublicIPv4 != "" {
		if err := ensurePublicIPv4(ctx, request.VirtualMachineID, request.UserID, request.PublicIPv4); err != nil {
			return err
		}
	}
	if request.Egress.HasVirtualEthernet() {
		if err := allocator.addMeshRegistration(ctx, request.VirtualMachineID, request.UserID, request.WireGuardMeshIPv6); err != nil {
			return err
		}
	}
	return configureTrafficControl(ctx, request.trafficControl())
}

func (allocator *LinuxAllocator) allocate(ctx context.Context, request request) (Interface, error) {
	exists, err := networkNamespaceExists(ctx, request.VirtualMachineID)
	if err != nil {
		return Interface{}, err
	}
	if !exists {
		if err := platform.Run(ctx, "ip", "netns", "add", namespaceName(request.VirtualMachineID)); err != nil {
			return Interface{}, err
		}
	}
	if err := allocator.ensureExisting(ctx, request); err != nil {
		return Interface{}, err
	}
	return allocator.resolve(request.VirtualMachineID), nil
}

func (allocator *LinuxAllocator) resolve(virtualMachineID string) Interface {
	return Interface{
		NetworkNamespacePath: namespacePath(virtualMachineID),
		TapName:              tapName,
		MACAddress:           guestMACAddress,
		GuestIPAddress:       guestIPAddress,
		GatewayIPAddress:     gatewayIPAddress,
	}
}

// Release removes a virtual machine network. It removes the mesh registration
// first, because deleting the namespace also deletes the veth pair that the
// registration names.
func (allocator *LinuxAllocator) Release(ctx context.Context, request ReleaseRequest) error {
	virtualMachineID := request.VirtualMachineID
	meshError := allocator.removeMeshRegistration(ctx, request.UserID, request.WireGuardMeshIPv6)
	rulesError := removePublicIPv4Rules(ctx, virtualMachineID)

	exists, err := networkNamespaceExists(ctx, virtualMachineID)
	if err != nil {
		return errors.Join(meshError, rulesError, err)
	}
	if !exists {
		return errors.Join(meshError, rulesError)
	}
	namespaceRulesError := removePublicIPv4NamespaceRules(ctx, virtualMachineID)

	namespaceError := platform.Run(ctx, "ip", "netns", "del", namespaceName(virtualMachineID))
	return errors.Join(meshError, rulesError, namespaceRulesError, namespaceError)
}

// addMeshRegistration routes the guest mesh address through the namespace and
// registers it with Atlas WG Mesh. The registration announces the VM location,
// so it comes last and the first packet that it attracts finds a complete path.
func (allocator *LinuxAllocator) addMeshRegistration(ctx context.Context, virtualMachineID string, userID uint32, address string) error {
	if address == "" {
		return nil
	}

	hostVirtualEthernet, guestVirtualEthernet := virtualEthernetNames(userID)
	if err := runSteps(ctx, meshNamespaceSteps(namespaceName(virtualMachineID), guestVirtualEthernet, address)); err != nil {
		return fmt.Errorf("route mesh address %s: %w", address, err)
	}
	if err := allocator.mesh.Add(ctx, address, hostVirtualEthernet); err != nil {
		return fmt.Errorf("register mesh address %s: %w", address, err)
	}
	return nil
}

// removeMeshRegistration unregisters the guest mesh address.
func (allocator *LinuxAllocator) removeMeshRegistration(ctx context.Context, userID uint32, address string) error {
	if address == "" {
		return nil
	}

	hostVirtualEthernet, _ := virtualEthernetNames(userID)
	if err := allocator.mesh.Remove(ctx, address, hostVirtualEthernet); err != nil {
		return fmt.Errorf("unregister mesh address %s: %w", address, err)
	}
	return nil
}

var _ vm.Network = (*LinuxAllocator)(nil)
