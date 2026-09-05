package network

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/frappe/atlas/metal/internal/hostcmd"
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
		if err := hostcmd.Run(ctx, "ip", "netns", "add", namespaceName(request.VirtualMachineID)); err != nil {
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

func ensureNamespaceBase(ctx context.Context, request request) error {
	namespace := namespaceName(request.VirtualMachineID)
	if err := hostcmd.Run(ctx, "ip", "-n", namespace, "link", "set", "lo", "up"); err != nil {
		return err
	}
	tapExists, err := namespaceLinkExists(ctx, namespace, tapName)
	if err != nil {
		return err
	}
	if !tapExists {
		userID, groupID := fmt.Sprint(request.UserID), fmt.Sprint(request.GroupID)
		if err := hostcmd.Run(ctx, "ip", "-n", namespace, "tuntap", "add", tapName, "mode", "tap", "user", userID, "group", groupID); err != nil {
			return err
		}
	}
	gatewayCIDR := fmt.Sprintf("%s/%d", gatewayIPAddress, networkPrefixLength)
	if err := hostcmd.Run(ctx, "ip", "-n", namespace, "addr", "replace", gatewayCIDR, "dev", tapName); err != nil {
		return err
	}
	return hostcmd.Run(ctx, "ip", "-n", namespace, "link", "set", tapName, "up")
}

func namespaceLinkExists(ctx context.Context, namespace, name string) (bool, error) {
	output, err := hostcmd.Output(ctx, "ip", "-n", namespace, "-o", "link", "show")
	if err != nil {
		return false, err
	}
	return linkListContains(output, name), nil
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

	namespaceError := hostcmd.Run(ctx, "ip", "netns", "del", namespaceName(virtualMachineID))
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

func namespaceName(virtualMachineID string) string { return "metal-" + virtualMachineID }

func namespacePath(virtualMachineID string) string {
	return "/run/netns/" + namespaceName(virtualMachineID)
}

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

// internetPathSteps builds the route out of the namespace. Only EgressUplink has it.
func internetPathSteps(namespace, guestVirtualEthernet, hostIPAddress string) [][]string {
	return append(defaultRouteSteps(namespace, hostIPAddress),
		[]string{"ip", "netns", "exec", namespace, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", guestVirtualEthernet, "-j", "MASQUERADE"},
	)
}

// defaultRouteSteps builds the namespace route and forwarding.
func defaultRouteSteps(namespace, hostIPAddress string) [][]string {
	return [][]string{
		{"ip", "-n", namespace, "route", "replace", "default", "via", hostIPAddress},
		{"ip", "netns", "exec", namespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1"},
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
		return hostcmd.Run(ctx, "ip", "link", "del", hostVirtualEthernet)
	}

	hostIPAddress, namespaceIPAddress := transitAddresses(userID)
	return runSteps(ctx, virtualEthernetSteps(namespace, hostVirtualEthernet, guestVirtualEthernet, hostIPAddress, namespaceIPAddress))
}

// addInternetPath adds the namespace route and NAT rule.
func addInternetPath(ctx context.Context, virtualMachineID string, userID uint32) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	hostIPAddress, _ := transitAddresses(userID)
	if err := runSteps(ctx, defaultRouteSteps(namespace, hostIPAddress)); err != nil {
		return err
	}
	return setMasquerade(ctx, namespace, guestVirtualEthernet, true)
}

// removeInternetPath removes the NAT rule before the route.
func removeInternetPath(ctx context.Context, virtualMachineID string, userID uint32) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	if err := setMasquerade(ctx, namespace, guestVirtualEthernet, false); err != nil {
		return err
	}
	return removeDefaultRoute(ctx, namespace)
}

// setMasquerade adds or removes the namespace NAT rule. iptables fails on a
// duplicate add and on a delete for an absent rule, so it checks first.
func setMasquerade(ctx context.Context, namespace, guestVirtualEthernet string, present bool) error {
	prefix := namespaceCommandPrefix(namespace)
	rule := []string{"POSTROUTING", "-o", guestVirtualEthernet, "-j", "MASQUERADE"}

	check := commandWithPrefix(prefix, "iptables", append([]string{"-t", "nat", "-C"}, rule...)...)
	exists, err := ruleExists(ctx, check)
	if err != nil || exists == present {
		return err
	}

	action := "-D"
	if present {
		action = "-A"
	}
	command := commandWithPrefix(prefix, "iptables", append([]string{"-t", "nat", action}, rule...)...)
	return hostcmd.Run(ctx, command[0], command[1:]...)
}

// ruleExists runs an iptables check. Exit code 1 means absent. Any other failure
// is an error, so a broken check does not read as absent.
func ruleExists(ctx context.Context, check []string) (bool, error) {
	err := hostcmd.Run(ctx, check[0], check[1:]...)
	if err == nil {
		return true, nil
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// removeDefaultRoute removes the namespace default route when it is present.
func removeDefaultRoute(ctx context.Context, namespace string) error {
	output, err := hostcmd.Output(ctx, "ip", "-n", namespace, "route", "show", "default")
	if err != nil {
		return fmt.Errorf("show default route: %w", err)
	}
	if strings.TrimSpace(output) == "" {
		return nil
	}
	return hostcmd.Run(ctx, "ip", "-n", namespace, "route", "del", "default")
}

// networkLinkExists reports whether one host network interface is present.
func networkLinkExists(ctx context.Context, name string) (bool, error) {
	output, err := hostcmd.Output(ctx, "ip", "-o", "link", "show")
	if err != nil {
		return false, fmt.Errorf("list network links: %w", err)
	}
	return linkListContains(output, name), nil
}

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

func runSteps(ctx context.Context, steps [][]string) error {
	for _, step := range steps {
		if err := hostcmd.Run(ctx, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func ensurePublicIPv4(ctx context.Context, virtualMachineID string, userID uint32, publicIPv4 string) error {
	namespace := namespaceName(virtualMachineID)
	_, guestVirtualEthernet := virtualEthernetNames(userID)
	_, namespaceIPAddress := transitAddresses(userID)
	steps := publicIPv4Steps(virtualMachineID, namespace, guestVirtualEthernet, namespaceIPAddress, publicIPv4)

	for _, step := range steps {
		exists, err := ruleExists(ctx, publicIPv4RuleCheck(step))
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := removePublicIPv4Rules(ctx, virtualMachineID); err != nil {
			return err
		}
		if err := removePublicIPv4NamespaceRules(ctx, virtualMachineID); err != nil {
			return err
		}
		return runSteps(ctx, steps)
	}
	return nil
}

func publicIPv4RuleCheck(step []string) []string {
	check := append([]string(nil), step...)
	for index, argument := range check {
		switch argument {
		case "-A":
			check[index] = "-C"
			return check
		case "-I":
			check[index] = "-C"
			return append(check[:index+2], check[index+3:]...)
		}
	}
	return check
}

func transitAddresses(userID uint32) (hostIPAddress, namespaceIPAddress string) {
	networkAddress := uint32(0x0A000000) | ((userID & 0x3FFFFF) << 2)
	return addressString(networkAddress + 1), addressString(networkAddress + 2)
}

func addressString(value uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

func networkNamespaceExists(ctx context.Context, virtualMachineID string) (bool, error) {
	output, err := hostcmd.Output(ctx, "ip", "netns", "list")
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

func publicIPv4Steps(virtualMachineID, namespace, guestVirtualEthernet, namespaceIPAddress, publicIPv4 string) [][]string {
	comment := publicIPv4Comment(virtualMachineID)
	return [][]string{
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-d", publicIPv4, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", namespaceIPAddress},
		{"iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", namespaceIPAddress, "-m", "comment", "--comment", comment, "-j", "SNAT", "--to-source", publicIPv4},
		{"iptables", "-A", "FORWARD", "-d", namespaceIPAddress, "-m", "conntrack", "--ctstate", "NEW,ESTABLISHED,RELATED", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-s", namespaceIPAddress, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},
		{"ip", "netns", "exec", namespace, "iptables", "-t", "nat", "-A", "PREROUTING", "-i", guestVirtualEthernet, "-d", namespaceIPAddress, "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", guestIPAddress},
	}
}

func removePublicIPv4Rules(ctx context.Context, virtualMachineID string) error {
	return removePublicIPv4RulesFrom(ctx, nil, virtualMachineID)
}

func removePublicIPv4NamespaceRules(ctx context.Context, virtualMachineID string) error {
	return removePublicIPv4RulesFrom(ctx, namespaceCommandPrefix(namespaceName(virtualMachineID)), virtualMachineID)
}

// namespaceCommandPrefix runs a host command inside one VM network namespace.
func namespaceCommandPrefix(namespace string) []string {
	return []string{"ip", "netns", "exec", namespace}
}

func removePublicIPv4RulesFrom(ctx context.Context, prefix []string, virtualMachineID string) error {
	var cleanupErrors []error
	for _, table := range []string{"nat", "filter"} {
		arguments := commandWithPrefix(prefix, "iptables", "-t", table, "-S")
		output, err := hostcmd.Output(ctx, arguments[0], arguments[1:]...)
		if err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("list %s rules: %w", table, err))
			continue
		}
		for _, line := range strings.Split(output, "\n") {
			ruleArguments := strings.Fields(line)
			if !hasRuleComment(ruleArguments, publicIPv4Comment(virtualMachineID)) {
				continue
			}
			if len(ruleArguments) < 2 || ruleArguments[0] != "-A" {
				continue
			}
			ruleArguments[0] = "-D"
			for index, argument := range ruleArguments {
				ruleArguments[index] = strings.Trim(argument, "\"")
			}
			arguments = commandWithPrefix(prefix, "iptables", "-t", table)
			arguments = append(arguments, ruleArguments...)
			if err := hostcmd.Run(ctx, arguments[0], arguments[1:]...); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s rule: %w", table, err))
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func commandWithPrefix(prefix []string, command string, arguments ...string) []string {
	result := append([]string(nil), prefix...)
	result = append(result, command)
	return append(result, arguments...)
}

func hasRuleComment(arguments []string, expected string) bool {
	for index, argument := range arguments {
		if argument == "--comment" && index+1 < len(arguments) {
			return strings.Trim(arguments[index+1], "\"") == expected
		}
	}
	return false
}

func publicIPv4Comment(virtualMachineID string) string {
	return "metal-public-ipv4-" + virtualMachineID
}

var _ vm.Network = (*LinuxAllocator)(nil)
