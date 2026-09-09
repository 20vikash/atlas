package network

import (
	"context"
	"fmt"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// ensureNamespaceBase creates the namespace loopback and guest tap.
func ensureNamespaceBase(ctx context.Context, request request) error {
	namespace := namespaceName(request.VirtualMachineID)
	if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "link", "set", "lo", "up"); err != nil {
		return err
	}
	tapExists, err := namespaceLinkExists(ctx, namespace, tapName)
	if err != nil {
		return err
	}
	if !tapExists {
		userID, groupID := fmt.Sprint(request.UserID), fmt.Sprint(request.GroupID)
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "tuntap", "add", tapName, "mode", "tap", "user", userID, "group", groupID); err != nil {
			return err
		}
	}
	gatewayCIDR := fmt.Sprintf("%s/%d", gatewayIPAddress, networkPrefixLength)
	if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "addr", "replace", gatewayCIDR, "dev", tapName); err != nil {
		return err
	}
	if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "link", "set", tapName, "up"); err != nil {
		return err
	}
	// Keep wake packets routable while the guest cannot answer ARP.
	_, err = platform.RunInNetworkNamespace(ctx, namespace, "ip", "neigh", "replace",
		guestIPAddress, "lladdr", guestMACAddress, "dev", tapName, "nud", "permanent")
	return err
}

// namespaceLinkExists reports whether one interface is inside the namespace.
func namespaceLinkExists(ctx context.Context, namespace, name string) (bool, error) {
	output, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", "-o", "link", "show")
	if err != nil {
		return false, err
	}
	return linkListContains(output, name), nil
}

// namespaceName is the network namespace of one virtual machine.
func namespaceName(virtualMachineID string) string { return "metal-" + virtualMachineID }

// namespacePath is the file a runtime enters to join the namespace.
func namespacePath(virtualMachineID string) string {
	return "/run/netns/" + namespaceName(virtualMachineID)
}

// virtualEthernetNames derives stable veth names from the user ID, so names need
// no stored state and survive daemon restarts.
func virtualEthernetNames(userID uint32) (host, guest string) {
	return fmt.Sprintf("vh-%d", userID), fmt.Sprintf("vg-%d", userID)
}

// virtualEthernetSteps builds the host side of the private network attachment.
func virtualEthernetSteps(namespace, hostVirtualEthernet, guestVirtualEthernet, hostIPAddress string) [][]string {
	return [][]string{
		{"ip", "link", "add", hostVirtualEthernet, "type", "veth", "peer", "name", guestVirtualEthernet},
		{"ip", "link", "set", guestVirtualEthernet, "netns", namespace},
		{"ip", "addr", "add", hostIPAddress + "/30", "dev", hostVirtualEthernet},
		{"ip", "link", "set", hostVirtualEthernet, "up"},
	}
}

// setVirtualEthernet adds or removes the veth pair. The pair belongs to this VM
// only when its peer sits in this VM's namespace. A released VM can leave a pair
// behind, and the next VM reuses its user ID and therefore its device names, so
// a host link alone is not proof that the pair is the right one.
func setVirtualEthernet(ctx context.Context, virtualMachineID string, userID uint32, present bool) error {
	namespace := namespaceName(virtualMachineID)
	hostVirtualEthernet, guestVirtualEthernet := virtualEthernetNames(userID)
	hostLinkExists, err := networkLinkExists(ctx, hostVirtualEthernet)
	if err != nil {
		return err
	}

	if !present {
		if !hostLinkExists {
			return nil
		}
		return platform.Run(ctx, "ip", "link", "del", hostVirtualEthernet)
	}

	attached, err := namespaceLinkExists(ctx, namespace, guestVirtualEthernet)
	if err != nil {
		return err
	}
	if attached {
		return nil
	}
	// The name is taken by a stale pair. Remove it, which removes both ends.
	if hostLinkExists {
		if err := platform.Run(ctx, "ip", "link", "del", hostVirtualEthernet); err != nil {
			return fmt.Errorf("remove stale veth %s: %w", hostVirtualEthernet, err)
		}
	}

	hostIPAddress, namespaceIPAddress := transitAddresses(userID)
	if err := runSteps(ctx, virtualEthernetSteps(namespace, hostVirtualEthernet, guestVirtualEthernet, hostIPAddress)); err != nil {
		return err
	}
	return runNetworkNamespaceSteps(ctx, namespace, [][]string{
		{"ip", "addr", "add", namespaceIPAddress + "/30", "dev", guestVirtualEthernet},
		{"ip", "link", "set", guestVirtualEthernet, "up"},
	})
}

// networkLinkExists reports whether one host network interface is present.
func networkLinkExists(ctx context.Context, name string) (bool, error) {
	output, err := platform.Output(ctx, "ip", "-o", "link", "show")
	if err != nil {
		return false, fmt.Errorf("list network links: %w", err)
	}
	return linkListContains(output, name), nil
}

// linkListContains finds a device in ip link output.
func linkListContains(output, name string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		device := strings.SplitN(strings.TrimSuffix(fields[1], ":"), "@", 2)[0]
		if device == name {
			return true
		}
	}
	return false
}

// runSteps runs host commands in order and stops at the first failure.
func runSteps(ctx context.Context, steps [][]string) error {
	for _, step := range steps {
		if err := platform.Run(ctx, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// runNetworkNamespaceSteps runs commands in one network namespace in order.
func runNetworkNamespaceSteps(ctx context.Context, namespace string, steps [][]string) error {
	for _, step := range steps {
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// transitAddresses returns the two usable addresses in the /30 derived from a
// user ID: one for the host and one for the namespace.
func transitAddresses(userID uint32) (hostIPAddress, namespaceIPAddress string) {
	networkAddress := uint32(0x0A000000) | ((userID & 0x3FFFFF) << 2)
	return addressString(networkAddress + 1), addressString(networkAddress + 2)
}

// addressString formats a packed IPv4 address.
func addressString(value uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

// networkNamespaceExists reports whether the namespace of one VM is present.
func networkNamespaceExists(ctx context.Context, virtualMachineID string) (bool, error) {
	output, err := platform.Output(ctx, "ip", "netns", "list")
	if err != nil {
		return false, fmt.Errorf("list network namespaces: %w", err)
	}

	name := namespaceName(virtualMachineID)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true, nil
		}
	}
	return false, nil
}
