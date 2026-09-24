package network

import (
	"context"
	"fmt"
	"slices"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

type routeFamily struct {
	flag           string
	nextHop        string
	defaultPrefix  string
	hostLength     string
	isDestination  func(vm.Route) bool
	ignoredPrefix  string
	forwardingStep []string
}

// routeFamilies defines the next hop for each address family.
func routeFamilies(userID uint32) []routeFamily {
	hostIPAddress, _ := transitAddresses(userID)
	return []routeFamily{
		{
			flag: "-4", nextHop: hostIPAddress, defaultPrefix: "0.0.0.0/0", hostLength: "/32",
			isDestination:  vm.Route.IsIPv4,
			forwardingStep: []string{"sysctl", "-q", "-w", "net.ipv4.ip_forward=1"},
		},
		{
			flag: "-6", nextHop: meshGatewayAddress, defaultPrefix: "::/0", hostLength: "/128",
			isDestination: func(route vm.Route) bool { return !route.IsIPv4() },
			// The mesh route belongs to the mesh registration, not to the VM routes.
			ignoredPrefix: meshPrefix,
		},
	}
}

// convergeNamespaceRoutes applies routes and IPv4 NAT inside the namespace.
func convergeNamespaceRoutes(ctx context.Context, request request) error {
	namespace := namespaceName(request.VirtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(request.UserID)
	for _, family := range routeFamilies(request.UserID) {
		if err := convergeFamilyRoutes(ctx, namespace, guestVirtualEthernet, family, request.Routes); err != nil {
			return err
		}
	}
	return setMasquerade(ctx, namespace, guestVirtualEthernet, request.HasIPv4HostRoute())
}

func convergeFamilyRoutes(ctx context.Context, namespace, guestVirtualEthernet string, family routeFamily, routes []vm.Route) error {
	var wanted []string
	for _, route := range routes {
		if family.isDestination(route) {
			wanted = append(wanted, route.Destination)
		}
	}

	output, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", family.flag, "route", "show",
		"via", family.nextHop, "dev", guestVirtualEthernet)
	if err != nil {
		return fmt.Errorf("read the namespace routes of %s: %w", namespace, err)
	}
	for _, destination := range parseNamespaceRoutes(output, family) {
		if slices.Contains(wanted, destination) {
			continue
		}
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", family.flag, "route", "del", destination,
			"via", family.nextHop, "dev", guestVirtualEthernet); err != nil {
			return fmt.Errorf("remove the namespace route of %s: %w", destination, err)
		}
	}
	if len(wanted) > 0 && family.forwardingStep != nil {
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, family.forwardingStep[0], family.forwardingStep[1:]...); err != nil {
			return fmt.Errorf("enable forwarding in %s: %w", namespace, err)
		}
	}
	for _, destination := range wanted {
		if _, err := platform.RunInNetworkNamespace(ctx, namespace, "ip", family.flag, "route", "replace", destination,
			"via", family.nextHop, "dev", guestVirtualEthernet); err != nil {
			return fmt.Errorf("add the namespace route of %s: %w", destination, err)
		}
	}
	return nil
}

// parseNamespaceRoutes restores prefixes that ip route abbreviates.
func parseNamespaceRoutes(output string, family routeFamily) []string {
	var destinations []string
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0 || fields[0] == family.ignoredPrefix:
		case fields[0] == "default":
			destinations = append(destinations, family.defaultPrefix)
		case !strings.Contains(fields[0], "/"):
			destinations = append(destinations, fields[0]+family.hostLength)
		default:
			destinations = append(destinations, fields[0])
		}
	}
	return destinations
}
