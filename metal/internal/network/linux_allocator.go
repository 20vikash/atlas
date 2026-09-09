package network

import (
	"context"
	"errors"
	"fmt"

	traffic "github.com/frappe/atlas/metal/internal/network/traffic"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// VMs reuse private addresses across isolated namespaces. The fixed guest MAC
// encodes 172.16.0.2 and keeps a stopped guest reachable.
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

// trafficMonitor attaches packet monitoring to VM TAP devices.
type trafficMonitor interface {
	Attach(traffic.AttachmentRequest) error
	Detach(string) error
}

// LinuxAllocator creates Linux network resources for virtual machines.
type LinuxAllocator struct {
	mesh           meshRegistrar
	trafficMonitor trafficMonitor
}

// NewLinuxAllocator returns a Linux network allocator.
func NewLinuxAllocator(mesh *Mesh, monitor *traffic.Monitor) *LinuxAllocator {
	var registrar meshRegistrar
	if mesh != nil {
		registrar = mesh
	}
	var trafficMonitor trafficMonitor
	if monitor != nil {
		trafficMonitor = monitor
	}
	return newLinuxAllocator(registrar, trafficMonitor)
}

// newLinuxAllocator returns an allocator with test dependencies.
func newLinuxAllocator(mesh meshRegistrar, trafficMonitor trafficMonitor) *LinuxAllocator {
	return &LinuxAllocator{mesh: mesh, trafficMonitor: trafficMonitor}
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
	if err := allocator.convergeTrafficMonitoring(desired.TrackTraffic, request); err != nil {
		return vm.NetworkInterface{}, err
	}

	return allocator.interfaceFor(request.VirtualMachineID), nil
}

// Release removes a VM network and its mesh registration. It unregisters mesh
// before deleting the namespace, which also deletes its veth pair.
func (allocator *LinuxAllocator) Release(ctx context.Context, request ReleaseRequest) error {
	virtualMachineID := request.VirtualMachineID
	// Release TCX links before tap0 is removed.
	var trafficError error
	if allocator.trafficMonitor != nil {
		trafficError = allocator.trafficMonitor.Detach(virtualMachineID)
	}
	meshError := allocator.removeMeshRegistration(ctx, request.UserID, request.WireGuardMeshIPv6)
	rulesError := removePublicIPv4Rules(ctx, virtualMachineID)

	exists, err := networkNamespaceExists(ctx, virtualMachineID)
	if err != nil {
		return errors.Join(trafficError, meshError, rulesError, err)
	}
	if !exists {
		return errors.Join(trafficError, meshError, rulesError)
	}
	namespaceRulesError := removePublicIPv4NamespaceRules(ctx, virtualMachineID)

	namespaceError := platform.Run(ctx, "ip", "netns", "del", namespaceName(virtualMachineID))
	return errors.Join(trafficError, meshError, rulesError, namespaceRulesError, namespaceError)
}

// convergeTrafficMonitoring attaches or detaches packet monitoring as requested.
func (allocator *LinuxAllocator) convergeTrafficMonitoring(enabled bool, request request) error {
	if allocator.trafficMonitor == nil {
		return nil
	}
	if enabled {
		err := allocator.trafficMonitor.Attach(traffic.AttachmentRequest{
			Target: traffic.Target{
				VirtualMachineID: request.VirtualMachineID,
				UserID:           request.UserID,
			},
			NamespacePath: namespacePath(request.VirtualMachineID),
			InterfaceName: tapName,
		})
		if err != nil {
			return fmt.Errorf("attach traffic monitor: %w", err)
		}
	} else if err := allocator.trafficMonitor.Detach(request.VirtualMachineID); err != nil {
		return fmt.Errorf("detach traffic monitor: %w", err)
	}
	return nil
}

// ensureNamespace creates the VM network namespace when it is absent.
func ensureNamespace(ctx context.Context, virtualMachineID string) error {
	exists, err := networkNamespaceExists(ctx, virtualMachineID)
	if err != nil || exists {
		return err
	}

	return platform.Run(ctx, "ip", "netns", "add", namespaceName(virtualMachineID))
}

// converge removes dropped resources before adding requested ones, so an egress
// mode change never leaves both network shapes in place.
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

// addWanted builds requested resources in dependency order. Mesh registration is
// last, so its first packet finds a complete path.
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

// addMeshRegistration routes the guest mesh address through the namespace and
// registers it with Atlas WG Mesh.
func (allocator *LinuxAllocator) addMeshRegistration(ctx context.Context, virtualMachineID string, userID uint32, address string) error {
	if allocator.mesh == nil || address == "" {
		return nil
	}

	hostVirtualEthernet, guestVirtualEthernet := virtualEthernetNames(userID)
	if err := runNetworkNamespaceSteps(ctx, namespaceName(virtualMachineID), meshNamespaceSteps(guestVirtualEthernet, address)); err != nil {
		return fmt.Errorf("route mesh address %s: %w", address, err)
	}
	if err := allocator.mesh.Add(ctx, address, hostVirtualEthernet); err != nil {
		return fmt.Errorf("register mesh address %s: %w", address, err)
	}
	return nil
}

// removeMeshRegistration unregisters the guest mesh address.
func (allocator *LinuxAllocator) removeMeshRegistration(ctx context.Context, userID uint32, address string) error {
	if allocator.mesh == nil || address == "" {
		return nil
	}

	hostVirtualEthernet, _ := virtualEthernetNames(userID)
	if err := allocator.mesh.Remove(ctx, address, hostVirtualEthernet); err != nil {
		return fmt.Errorf("unregister mesh address %s: %w", address, err)
	}
	return nil
}

var _ vm.Network = (*LinuxAllocator)(nil)
