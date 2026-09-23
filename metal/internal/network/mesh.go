package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// meshMTU is the VM interface MTU required by Atlas WG Mesh. It leaves room for
// the outer IPv6 header inside the WireGuard MTU.
const meshMTU = 1380

// meshGatewayAddress is the link-local address the guest routes the mesh through.
const meshGatewayAddress = "fe80::1"

// meshPrefix is the Atlas mesh address block.
const meshPrefix = "fdaa::/16"

// MeshConfig identifies the Atlas WG Mesh CLI and the host interfaces it uses.
// UplinkName must name the shared VLAN interface that carries Atlas NDP, never
// its parent, because the NDP hook and the proxy NDP entries attach to it.
type MeshConfig struct {
	CommandPath   string
	UplinkName    string
	WireGuardName string
	// WireGuardStatePath holds the managed WireGuard peer state that the mesh reads.
	WireGuardStatePath string
}

// Mesh registers virtual machine addresses with the Atlas WG Mesh CLI.
type Mesh struct {
	commandPath        string
	uplinkName         string
	wireGuardName      string
	wireGuardStatePath string
}

// NewMesh returns a mesh registrar for one host.
func NewMesh(configuration MeshConfig) (*Mesh, error) {
	if configuration.CommandPath == "" {
		return nil, errors.New("Atlas WG Mesh command path is required")
	}
	if configuration.WireGuardName == "" {
		return nil, errors.New("WireGuard interface name is required")
	}
	if configuration.UplinkName == "" {
		return nil, errors.New("Atlas WG Mesh uplink interface name is required")
	}
	if configuration.WireGuardStatePath == "" {
		return nil, errors.New("Atlas WG Mesh peer state path is required")
	}
	if _, err := exec.LookPath(configuration.CommandPath); err != nil {
		return nil, fmt.Errorf("Atlas WG Mesh CLI %s: %w", configuration.CommandPath, err)
	}

	return &Mesh{
		commandPath:        configuration.CommandPath,
		uplinkName:         configuration.UplinkName,
		wireGuardName:      configuration.WireGuardName,
		wireGuardStatePath: configuration.WireGuardStatePath,
	}, nil
}

// Check the mesh registrar interface at compile time.
var _ meshRegistrar = (*Mesh)(nil)

// SyncPeerState replaces the BPF peer map and its NDP transport mode.
func (mesh *Mesh) SyncPeerState(ctx context.Context, unicast bool) error {
	arguments := []string{"peers", "sync", mesh.wireGuardStatePath}
	if unicast {
		arguments = append(arguments, "--unicast")
	}
	return platform.Run(ctx, mesh.commandPath, arguments...)
}

// PrivateNetworkMAC returns the MAC address of the mesh uplink.
// It reads one sysfs file, because every sync calls it and an interface lookup lists every link.
func (mesh *Mesh) PrivateNetworkMAC() (string, error) {
	contents, err := os.ReadFile("/sys/class/net/" + mesh.uplinkName + "/address")
	if err != nil {
		return "", fmt.Errorf("read uplink %s: %w", mesh.uplinkName, err)
	}
	mac, err := net.ParseMAC(strings.TrimSpace(string(contents)))
	if err != nil || len(mac) != 6 {
		return "", fmt.Errorf("uplink %s has no Ethernet MAC address", mesh.uplinkName)
	}
	return mac.String(), nil
}

// EnsureHost applies the host configuration and refreshes its BPF programs.
func (mesh *Mesh) EnsureHost(ctx context.Context) error {
	return platform.Run(ctx, mesh.commandPath, "configure",
		"--uplink", mesh.uplinkName, "--wireguard", mesh.wireGuardName)
}

// removeVM unregisters one VM address. An address this host does not own is not an error.
func (mesh *Mesh) removeVM(ctx context.Context, address, interfaceName string) error {
	return platform.Run(ctx, mesh.commandPath, "vm", "remove", "--interface", interfaceName, "--address", address)
}

// ApplyPrivilegedAddresses replaces the complete privileged VM whitelist.
func (mesh *Mesh) ApplyPrivilegedAddresses(ctx context.Context, desired []string) error {
	arguments := []string{"privileged-vm", "clear"}
	if len(desired) > 0 {
		arguments = append([]string{"privileged-vm", "replace"}, desired...)
	}
	return platform.Run(ctx, mesh.commandPath, arguments...)
}

// meshNamespaceSteps routes mesh traffic through the namespace. The guest
// owns its mesh address, and the proxy NDP entry answers for it from
// creation, before the guest applies its own copy. A permanent tap0 neighbour
// reaches a stopped guest. A zero proxy_delay answers the host at once, because
// the mesh drops packets until the host learns the guest.
func meshNamespaceSteps(guestVirtualEthernet, address string) [][]string {
	return [][]string{
		{"sysctl", "-q", "-w", "net.ipv6.conf.all.forwarding=1"},
		{"sysctl", "-q", "-w", "net.ipv6.conf." + guestVirtualEthernet + ".proxy_ndp=1"},
		{"sysctl", "-q", "-w", "net.ipv6.neigh." + guestVirtualEthernet + ".proxy_delay=0"},
		{"ip", "link", "set", guestVirtualEthernet, "mtu", strconv.Itoa(meshMTU)},
		{"ip", "-6", "addr", "replace", meshGatewayAddress + "/64", "dev", tapName, "nodad"},
		{"ip", "-6", "route", "replace", address + "/128", "dev", tapName},
		{"ip", "-6", "neigh", "replace", address, "lladdr", guestMACAddress, "dev", tapName, "nud", "permanent"},
		{"ip", "-6", "route", "replace", meshPrefix, "via", meshGatewayAddress, "dev", guestVirtualEthernet},
		{"ip", "-6", "neigh", "replace", "proxy", address, "dev", guestVirtualEthernet},
	}
}

// syncVM applies the host routes first and registers the complete VM state last.
func (mesh *Mesh) syncVM(ctx context.Context, request request) error {
	if request.WireGuardMeshIPv6 == "" {
		return nil
	}
	hostVirtualEthernet, _ := virtualEthernetNames(request.UserID)
	if err := mesh.convergeNamespaceRoutes(ctx, request); err != nil {
		return err
	}
	if err := mesh.convergeOwnedPrefixRoutes(ctx, request); err != nil {
		return err
	}

	arguments := []string{
		"vm", "sync", "--interface", hostVirtualEthernet,
		"--address", request.WireGuardMeshIPv6, "--mtu", strconv.Itoa(meshMTU),
	}
	if request.IsNetworkGateway {
		arguments = append(arguments, "--gateway")
	}
	if request.PublicIPv6 != "" {
		arguments = append(arguments, "--prefix", request.PublicIPv6)
	}
	for _, route := range request.GatewayRoutes {
		arguments = append(arguments, "--route", route.Destination+"="+route.Gateway)
	}
	return platform.Run(ctx, mesh.commandPath, arguments...)
}

// convergeNamespaceRoutes sends each configured destination through the host.
func (mesh *Mesh) convergeNamespaceRoutes(ctx context.Context, request request) error {
	_, guestVirtualEthernet := virtualEthernetNames(request.UserID)
	wanted := vm.HostReachedDestinations(vm.NetworkConfiguration{
		GatewayRoutes: request.GatewayRoutes, PublicIPv6: request.PublicIPv6, IsNetworkGateway: request.IsNetworkGateway,
	})
	present, err := mesh.namespaceRoutes(ctx, request)
	if err != nil {
		return err
	}

	namespace := namespaceName(request.VirtualMachineID)
	for _, destination := range present {
		if slices.Contains(wanted, destination) {
			continue
		}
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "-6", "route", "del", destination,
			"via", meshGatewayAddress, "dev", guestVirtualEthernet); err != nil {
			return fmt.Errorf("remove the namespace route of %s: %w", destination, err)
		}
	}
	for _, destination := range wanted {
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "-6", "route", "replace", destination,
			"via", meshGatewayAddress, "dev", guestVirtualEthernet); err != nil {
			return fmt.Errorf("add the namespace route of %s: %w", destination, err)
		}
	}
	return nil
}

func (mesh *Mesh) namespaceRoutes(ctx context.Context, request request) ([]string, error) {
	_, guestVirtualEthernet := virtualEthernetNames(request.UserID)
	output, err := platform.RunInNetworkNamespace(ctx, namespaceName(request.VirtualMachineID),
		"ip", "-6", "route", "show", "via", meshGatewayAddress, "dev", guestVirtualEthernet)
	if err != nil {
		return nil, fmt.Errorf("read the namespace routes of %s: %w", request.VirtualMachineID, err)
	}

	return parseNamespaceRoutes(output), nil
}

// parseNamespaceRoutes returns the destinations of ip route output, except the mesh route. ip prints ::/0 as default.
func parseNamespaceRoutes(output string) []string {
	var destinations []string
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0 || fields[0] == meshPrefix:
		case fields[0] == "default":
			destinations = append(destinations, "::/0")
		default:
			destinations = append(destinations, fields[0])
		}
	}
	return destinations
}

// convergeOwnedPrefixRoutes sends each public prefix to its gateway VM.
func (mesh *Mesh) convergeOwnedPrefixRoutes(ctx context.Context, request request) error {
	hostVirtualEthernet, _ := virtualEthernetNames(request.UserID)
	present, err := platform.Output(ctx, "ip", "-6", "route", "show", "dev", hostVirtualEthernet)
	if err != nil {
		return fmt.Errorf("read the routes of %s: %w", hostVirtualEthernet, err)
	}

	for line := range strings.SplitSeq(present, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fieldAfter(fields, "via") != request.WireGuardMeshIPv6 || fields[0] == request.PublicIPv6 {
			continue
		}
		if err := mesh.changeOwnedPrefixRoute(ctx, request, "del", fields[0]); err != nil {
			return err
		}
	}
	if request.PublicIPv6 != "" {
		return mesh.changeOwnedPrefixRoute(ctx, request, "replace", request.PublicIPv6)
	}
	return nil
}

func (mesh *Mesh) changeOwnedPrefixRoute(ctx context.Context, request request, action, prefix string) error {
	hostVirtualEthernet, _ := virtualEthernetNames(request.UserID)
	if err := platform.Run(ctx, "ip", "-6", "route", action, prefix,
		"via", request.WireGuardMeshIPv6, "dev", hostVirtualEthernet); err != nil {
		return fmt.Errorf("%s the route of %s: %w", action, prefix, err)
	}
	if _, err := platform.RunInNetworkNamespace(ctx, namespaceName(request.VirtualMachineID),
		"ip", "-6", "route", action, prefix, "via", request.WireGuardMeshIPv6, "dev", tapName); err != nil {
		return fmt.Errorf("%s the guest route of %s: %w", action, prefix, err)
	}
	return nil
}

func fieldAfter(fields []string, key string) string {
	for index, field := range fields {
		if field == key && index+1 < len(fields) {
			return fields[index+1]
		}
	}
	return ""
}
