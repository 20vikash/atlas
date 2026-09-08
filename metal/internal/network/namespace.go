package network

import (
	"context"
	"fmt"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// ensureNamespaceBase brings up loopback and the tap the guest attaches to. The
// tap is owned by the VM user, so the jailed Firecracker process can open it.
func ensureNamespaceBase(ctx context.Context, request request) error {
	namespace := namespaceName(request.VirtualMachineID)
	if err := platform.Run(ctx, "ip", "-n", namespace, "link", "set", "lo", "up"); err != nil {
		return err
	}
	tapExists, err := namespaceLinkExists(ctx, namespace, tapName)
	if err != nil {
		return err
	}
	if !tapExists {
		userID, groupID := fmt.Sprint(request.UserID), fmt.Sprint(request.GroupID)
		if err := platform.Run(ctx, "ip", "-n", namespace, "tuntap", "add", tapName, "mode", "tap", "user", userID, "group", groupID); err != nil {
			return err
		}
	}
	gatewayCIDR := fmt.Sprintf("%s/%d", gatewayIPAddress, networkPrefixLength)
	if err := platform.Run(ctx, "ip", "-n", namespace, "addr", "replace", gatewayCIDR, "dev", tapName); err != nil {
		return err
	}
	return platform.Run(ctx, "ip", "-n", namespace, "link", "set", tapName, "up")
}

// namespaceLinkExists reports whether one interface is inside the namespace.
func namespaceLinkExists(ctx context.Context, namespace, name string) (bool, error) {
	output, err := platform.Output(ctx, "ip", "-n", namespace, "-o", "link", "show")
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

// virtualEthernetNames derives both veth ends from the user ID, so the names
// stay stable across restarts without any stored state.
func virtualEthernetNames(userID uint32) (host, guest string) {
	return fmt.Sprintf("vh-%d", userID), fmt.Sprintf("vg-%d", userID)
}

// virtualEthernetSteps builds the private network attachment. Only EgressNone drops it.
func virtualEthernetSteps(namespace, hostVirtualEthernet, guestVirtualEthernet, hostIPAddress, namespaceIPAddress string) [][]string {
	return [][]string{
		{"ip", "link", "add", hostVirtualEthernet, "type", "veth", "peer", "name", guestVirtualEthernet},
		{"ip", "link", "set", guestVirtualEthernet, "netns", namespace},
		{"ip", "addr", "add", hostIPAddress + "/30", "dev", hostVirtualEthernet},
		{"ip", "link", "set", hostVirtualEthernet, "up"},
		{"ip", "-n", namespace, "addr", "add", namespaceIPAddress + "/30", "dev", guestVirtualEthernet},
		{"ip", "-n", namespace, "link", "set", guestVirtualEthernet, "up"},
	}
}

// setVirtualEthernet adds or removes the veth pair.
func setVirtualEthernet(ctx context.Context, virtualMachineID string, userID uint32, present bool) error {
	namespace := namespaceName(virtualMachineID)
	hostVirtualEthernet, guestVirtualEthernet := virtualEthernetNames(userID)
	exists, err := networkLinkExists(ctx, hostVirtualEthernet)
	if err != nil || exists == present {
		return err
	}
	if !present {
		return platform.Run(ctx, "ip", "link", "del", hostVirtualEthernet)
	}

	hostIPAddress, namespaceIPAddress := transitAddresses(userID)
	return runSteps(ctx, virtualEthernetSteps(namespace, hostVirtualEthernet, guestVirtualEthernet, hostIPAddress, namespaceIPAddress))
}

// networkLinkExists reports whether one host network interface is present.
func networkLinkExists(ctx context.Context, name string) (bool, error) {
	output, err := platform.Output(ctx, "ip", "-o", "link", "show")
	if err != nil {
		return false, fmt.Errorf("list network links: %w", err)
	}
	return linkListContains(output, name), nil
}

// linkListContains finds a device in `ip link show` output. A veth line names
// the peer after an @, which is trimmed.
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

// transitAddresses carves one /30 out of 10.0.0.0/8 per user ID and returns its
// 2 usable addresses: the host end and the namespace end. The ID is masked to 22
// bits, which shifted by 2 fills the 24 host bits of the /8.
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
