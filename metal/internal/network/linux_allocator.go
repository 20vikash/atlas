package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/frappe/atlas/metal/internal/network/activity"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// VMs reuse private addresses across isolated namespaces.
const (
	tapName             = "tap0"
	gatewayIPAddress    = "172.16.0.1"
	guestIPAddress      = "172.16.0.2"
	networkPrefixLength = 24
	guestMACAddress     = "06:00:ac:10:00:02"
)

// meshRegistrar registers and unregisters guest mesh addresses.
type meshRegistrar interface {
	Add(ctx context.Context, address, interfaceName string) error
	Remove(ctx context.Context, address, interfaceName string) error
}

// activityAttacher manages VM packet activity tracking.
type activityAttacher interface {
	EnsureAttachment(request activity.AttachmentRequest) error
	ReleaseAttachment(virtualMachineID string) error
	LastNetworkActivity(ctx context.Context, request vm.NetworkActivityRequest) (vm.NetworkActivity, error)
}

// LinuxAllocator creates Linux network resources for virtual machines.
type LinuxAllocator struct {
	mesh     meshRegistrar
	activity activityAttacher
}

// NewLinuxAllocator returns a Linux network allocator.
func NewLinuxAllocator(mesh meshRegistrar, attacher activityAttacher) *LinuxAllocator {
	return &LinuxAllocator{mesh: mesh, activity: attacher}
}

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
	if err := ensureNamespace(ctx, request.VirtualMachineID); err != nil {
		return vm.NetworkInterface{}, err
	}
	if err := allocator.converge(ctx, request); err != nil {
		return vm.NetworkInterface{}, err
	}
	if err := allocator.activity.EnsureAttachment(activity.AttachmentRequest{
		VirtualMachineID: request.VirtualMachineID,
		UserID:           request.UserID,
		NamespacePath:    namespacePath(request.VirtualMachineID),
		TapName:          tapName,
	}); err != nil {
		return vm.NetworkInterface{}, fmt.Errorf("attach activity tracking: %w", err)
	}

	return allocator.interfaceFor(request.VirtualMachineID), nil
}

// ensureNamespace creates the VM network namespace when it is absent.
func ensureNamespace(ctx context.Context, virtualMachineID string) error {
	exists, err := networkNamespaceExists(ctx, virtualMachineID)
	if err != nil || exists {
		return err
	}

	return platform.Run(ctx, "ip", "netns", "add", namespaceName(virtualMachineID))
}

// converge removes dropped resources before adding requested ones.
func (allocator *LinuxAllocator) converge(ctx context.Context, request request) error {
	if request.PublicIPv4 != "" && !request.Egress.HasInternetPath() {
		return fmt.Errorf("public IPv4 requires %s egress", vm.EgressUplink)
	}

	if err := ensureNamespaceBase(ctx, request); err != nil {
		return err
	}
	if err := allocator.removeUnwanted(ctx, request); err != nil {
		return err
	}
	if err := allocator.addWanted(ctx, request); err != nil {
		return err
	}

	return configureTrafficControl(ctx, request.trafficControl())
}

// removeUnwanted tears down what this request no longer asks for.
func (allocator *LinuxAllocator) removeUnwanted(ctx context.Context, request request) error {
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

	return nil
}

// addWanted builds requested resources in dependency order.
func (allocator *LinuxAllocator) addWanted(ctx context.Context, request request) error {
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

	return nil
}

// interfaceFor returns the values a runtime needs to attach one VM.
func (allocator *LinuxAllocator) interfaceFor(virtualMachineID string) Interface {
	return Interface{
		NetworkNamespacePath: namespacePath(virtualMachineID),
		TapName:              tapName,
		MACAddress:           guestMACAddress,
		GuestIPAddress:       guestIPAddress,
		GatewayIPAddress:     gatewayIPAddress,
	}
}

// Release removes a VM network and its mesh registration.
func (allocator *LinuxAllocator) Release(ctx context.Context, request ReleaseRequest) error {
	virtualMachineID := request.VirtualMachineID
	// Release TCX links before tap0 is removed.
	activityError := allocator.activity.ReleaseAttachment(virtualMachineID)
	meshError := allocator.removeMeshRegistration(ctx, request.UserID, request.WireGuardMeshIPv6)
	rulesError := removePublicIPv4Rules(ctx, virtualMachineID)

	exists, err := networkNamespaceExists(ctx, virtualMachineID)
	if err != nil {
		return errors.Join(activityError, meshError, rulesError, err)
	}
	if !exists {
		return errors.Join(activityError, meshError, rulesError)
	}
	namespaceRulesError := removePublicIPv4NamespaceRules(ctx, virtualMachineID)

	namespaceError := platform.Run(ctx, "ip", "netns", "del", namespaceName(virtualMachineID))
	return errors.Join(activityError, meshError, rulesError, namespaceRulesError, namespaceError)
}

// addMeshRegistration routes and registers the guest mesh address.
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

// LastNetworkActivity returns the last packet time for one VM.
func (allocator *LinuxAllocator) LastNetworkActivity(ctx context.Context, request vm.NetworkActivityRequest) (vm.NetworkActivity, error) {
	return allocator.activity.LastNetworkActivity(ctx, request)
}

var (
	_ vm.Network                = (*LinuxAllocator)(nil)
	_ vm.NetworkActivityMonitor = (*LinuxAllocator)(nil)
)
