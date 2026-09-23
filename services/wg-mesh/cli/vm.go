package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// movedPrefixLifetime bounds forwarding through a VM's previous host.
// The first forwarded packet makes the new host advertise the address.
const movedPrefixLifetime = 5 * time.Minute

var (
	vmInterfaceName string
	vmAddress       string
	vmMTU           uint32
	vmIsGateway     bool
	vmPrefixes      []string
	vmRoutes        []string
	listJSON        bool
)

var vmCommand = &cobra.Command{Use: "vm", Short: "manage local VMs", Args: cobra.NoArgs}

var syncVMCommand = &cobra.Command{
	Use: "sync", Short: "replace the configuration of one local VM", Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return syncVM(vmInterfaceName, vmAddress, vmMTU, vmIsGateway, vmPrefixes, vmRoutes)
	},
}

var removeVMCommand = &cobra.Command{
	Use: "remove", Short: "remove one local VM", Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error { return removeVM(vmInterfaceName, vmAddress) },
}

var listVMsCommand = &cobra.Command{
	Use: "list", Short: "list local VMs", Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error { return listVMs() },
}

var privilegedVMCommand = &cobra.Command{Use: "privileged-vm", Short: "manage privileged tenant-0 VMs", Args: cobra.NoArgs}

var replacePrivilegedVMsCommand = &cobra.Command{
	Use: "replace ADDRESS...", Short: "replace the complete privileged VM set", Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, addresses []string) error { return replacePrivilegedVMs(addresses) },
}

var clearPrivilegedVMsCommand = &cobra.Command{
	Use: "clear", Short: "clear the privileged VM set", Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error { return replacePrivilegedVMs(nil) },
}

var listPrivilegedVMsCommand = &cobra.Command{
	Use: "list", Short: "list privileged VMs", Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error { return listPrivilegedVMs() },
}

func init() {
	for _, command := range []*cobra.Command{syncVMCommand, removeVMCommand} {
		command.Flags().StringVar(&vmInterfaceName, "interface", "", "VM interface")
		command.Flags().StringVar(&vmAddress, "address", "", "VM mesh address")
		command.MarkFlagRequired("interface")
		command.MarkFlagRequired("address")
	}
	syncVMCommand.Flags().Uint32Var(&vmMTU, "mtu", 1380, "VM interface MTU")
	syncVMCommand.Flags().BoolVar(&vmIsGateway, "gateway", false, "make this VM the host gateway")
	syncVMCommand.Flags().StringArrayVar(&vmPrefixes, "prefix", nil, "public IPv6 prefix owned by this VM")
	syncVMCommand.Flags().StringArrayVar(&vmRoutes, "route", nil, "destination=gateway")
	listVMsCommand.Flags().BoolVar(&listJSON, "json", false, "print JSON")
	listPrivilegedVMsCommand.Flags().BoolVar(&listJSON, "json", false, "print JSON")

	vmCommand.AddCommand(syncVMCommand, removeVMCommand, listVMsCommand)
	privilegedVMCommand.AddCommand(replacePrivilegedVMsCommand, clearPrivilegedVMsCommand, listPrivilegedVMsCommand)
	rootCommand.AddCommand(vmCommand, privilegedVMCommand)
}

type vmState struct {
	address  [16]byte
	prefixes map[prefixKey]bool
	routes   map[routeKey][16]byte
}

func parseVMState(addressText string, prefixTexts, routeTexts []string) (vmState, error) {
	address, err := parseMeshAddress(addressText)
	if err != nil {
		return vmState{}, err
	}

	state := vmState{address: address, prefixes: make(map[prefixKey]bool), routes: make(map[routeKey][16]byte)}
	for _, text := range prefixTexts {
		prefix, err := parsePrefix(text)
		if err != nil {
			return vmState{}, err
		}
		state.prefixes[prefix] = true
	}
	for _, text := range routeTexts {
		destinationText, gatewayText, found := strings.Cut(text, "=")
		if !found {
			return vmState{}, fmt.Errorf("route %q is not destination=gateway", text)
		}
		destination, err := parsePrefix(destinationText)
		if err != nil {
			return vmState{}, err
		}
		gatewayAddress, err := parseMeshAddress(gatewayText)
		if err != nil {
			return vmState{}, err
		}
		key := routeKey{128 + destination.PrefixLength, address, destination.Address}
		if _, exists := state.routes[key]; exists {
			return vmState{}, fmt.Errorf("route for %s is repeated", destinationText)
		}
		state.routes[key] = gatewayAddress
	}

	return state, nil
}

// syncVM applies the complete state of one VM. A repeat repairs a partial operation.
func syncVM(interfaceName, addressText string, mtu uint32, isGateway bool, prefixTexts, routeTexts []string) error {
	state, err := parseVMState(addressText, prefixTexts, routeTexts)
	if err != nil {
		return err
	}
	if mtu < 1280 || mtu > 65535 {
		return fmt.Errorf("MTU %d is outside 1280 through 65535", mtu)
	}

	unlock, err := lockFile(vmLockPath, true)
	if err != nil {
		return err
	}
	defer unlock()

	device, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return err
	}
	ifindex := uint32(device.Index)
	config, err := readPinnedConfig()
	if err != nil {
		return err
	}
	if err := validateVMState(state, ifindex); err != nil {
		return err
	}

	if err := attachHook(interfaceName, vmProgram, "ingress"); err != nil {
		return err
	}
	address := netip.AddrFrom16(state.address).String()
	for _, arguments := range [][]string{
		{"-6", "addr", "replace", "fe80::1/64", "dev", interfaceName, "nodad"},
		{"link", "set", interfaceName, "mtu", fmt.Sprint(mtu), "up"},
		{"-6", "route", "replace", address + "/128", "dev", interfaceName},
	} {
		if err := runCommand("ip", arguments...); err != nil {
			return err
		}
	}

	if err := writeMap("local_vms", state.address, ifindex); err != nil {
		return err
	}
	if err := applyPrefixes(ifindex, state.prefixes); err != nil {
		return err
	}
	if err := pruneMovedPrefixes(state.prefixes); err != nil {
		return err
	}
	if err := applyRoutes(state.address, state.routes); err != nil {
		return err
	}
	if err := applyGateway(ifindex, isGateway); err != nil {
		return err
	}
	if err := runCommand("ip", "-6", "neigh", "replace", "proxy", address, "dev", deviceName(config.UplinkIfIndex)); err != nil {
		return err
	}
	if err := announceVMLocation(config, state.address); err != nil {
		return err
	}

	fmt.Printf("VM %s is ready on %s\n", address, interfaceName)
	return nil
}

func validateVMState(state vmState, ifindex uint32) error {
	owners, err := readMapEntries[prefixKey, uint32]("owned_prefixes")
	if err != nil {
		return err
	}
	for prefix := range state.prefixes {
		if owner := owners[prefix]; owner != 0 && owner != ifindex {
			return fmt.Errorf("%s already owns %s", deviceName(owner), prefixText(prefix))
		}
	}
	return nil
}

func applyGateway(ifindex uint32, enabled bool) error {
	if enabled {
		return writeMap("gateways", ifindex, uint8(1))
	}
	return deleteMapKey("gateways", ifindex)
}

func applyPrefixes(ifindex uint32, wanted map[prefixKey]bool) error {
	owners, err := readMapEntries[prefixKey, uint32]("owned_prefixes")
	if err != nil {
		return err
	}
	for prefix, owner := range owners {
		if owner == ifindex && !wanted[prefix] {
			if err := deleteMapKey("owned_prefixes", prefix); err != nil {
				return err
			}
		}
	}
	for prefix := range wanted {
		if err := writeMap("owned_prefixes", prefix, ifindex); err != nil {
			return err
		}
	}
	return nil
}

// rememberMovedPrefixes keeps the blocks of a VM that leaves, so the public hook can forward their traffic to its next host.
func rememberMovedPrefixes(ifindex uint32, address [16]byte) error {
	owners, err := readMapEntries[prefixKey, uint32]("owned_prefixes")
	if err != nil {
		return err
	}
	now, err := monotonicNanoseconds()
	if err != nil {
		return err
	}
	for prefix, owner := range owners {
		if owner != ifindex {
			continue
		}
		if err := writeMap("moved_prefixes", prefix, movedPrefix{address, now + uint64(movedPrefixLifetime)}); err != nil {
			return err
		}
	}
	return nil
}

// pruneMovedPrefixes removes blocks that returned to this host or expired.
func pruneMovedPrefixes(owned map[prefixKey]bool) error {
	moved, err := readMapEntries[prefixKey, movedPrefix]("moved_prefixes")
	if err != nil {
		return err
	}
	now, err := monotonicNanoseconds()
	if err != nil {
		return err
	}
	for prefix, entry := range moved {
		if owned[prefix] || entry.ExpiresNS <= now {
			if err := deleteMapKey("moved_prefixes", prefix); err != nil {
				return err
			}
		}
	}
	return nil
}

func monotonicNanoseconds() (uint64, error) {
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		return 0, err
	}
	return uint64(now.Nano()), nil
}

// announceVMLocation lets every peer learn the VM's host without a NOT_HERE round trip.
func announceVMLocation(config hostConfig, address [16]byte) error {
	socket, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
	if err != nil {
		return fmt.Errorf("open the NDP socket: %w", err)
	}
	defer unix.Close(socket)

	uplink := int(config.UplinkIfIndex)
	if err := unix.SetsockoptInt(socket, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, 255); err != nil {
		return err
	}
	if err := unix.SetsockoptInt(socket, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, uplink); err != nil {
		return err
	}

	// Neighbor advertisement with the override flag, then the target MAC option. Linux fills the checksum.
	message := []byte{136, 0, 0, 0, 0x20, 0, 0, 0}
	message = append(message, address[:]...)
	message = append(message, 2, 1)
	message = append(message, config.UplinkMAC[:]...)
	allNodes := &unix.SockaddrInet6{ZoneId: uint32(uplink), Addr: [16]byte{0: 0xff, 1: 0x02, 15: 1}}
	if err := unix.Sendto(socket, message, 0, allNodes); err != nil {
		// The unicast hook clones the packet to every peer, then drops the original.
		if errors.Is(err, unix.ENOBUFS) && isHookAttached(deviceName(config.UplinkIfIndex), "egress") {
			return nil
		}
		return fmt.Errorf("announce VM location %s: %w", netip.AddrFrom16(address), err)
	}
	return nil
}

func applyRoutes(address [16]byte, wanted map[routeKey][16]byte) error {
	routes, err := readMapEntries[routeKey, [16]byte]("gateway_routes")
	if err != nil {
		return err
	}
	for key := range routes {
		if _, keep := wanted[key]; key.VirtualMachine == address && !keep {
			if err := deleteMapKey("gateway_routes", key); err != nil {
				return err
			}
		}
	}
	for key, gatewayAddress := range wanted {
		if err := writeMap("gateway_routes", key, gatewayAddress); err != nil {
			return err
		}
	}
	return nil
}

// removeVM clears all BPF state owned by one VM before it removes the hook.
func removeVM(interfaceName, addressText string) error {
	address, err := parseMeshAddress(addressText)
	if err != nil {
		return err
	}

	unlock, err := lockFile(vmLockPath, true)
	if err != nil {
		return err
	}
	defer unlock()

	config, err := readPinnedConfig()
	if err != nil {
		return err
	}
	vms, err := readLocalVMs()
	if err != nil {
		return err
	}
	ifindex, registered := vms[address]
	interfaceExists := true
	if registered {
		device, lookupError := net.InterfaceByIndex(int(ifindex))
		interfaceExists = lookupError == nil
		if interfaceExists && device.Name != interfaceName {
			return fmt.Errorf("VM %s is on %s, not %s", netip.AddrFrom16(address), device.Name, interfaceName)
		}
	}

	canonicalAddress := netip.AddrFrom16(address).String()
	if err := ignoreMissing(runCommand("ip", "-6", "neigh", "del", "proxy", canonicalAddress, "dev", deviceName(config.UplinkIfIndex))); err != nil {
		return err
	}
	if registered {
		if err := applyGateway(ifindex, false); err != nil {
			return err
		}
		if err := rememberMovedPrefixes(ifindex, address); err != nil {
			return err
		}
		if err := applyPrefixes(ifindex, nil); err != nil {
			return err
		}
	}
	if err := applyRoutes(address, nil); err != nil {
		return err
	}
	if err := deleteMapKey("local_vms", address); err != nil {
		return err
	}
	if !registered || interfaceExists {
		if err := ignoreMissing(runCommand("ip", "-6", "route", "del", canonicalAddress+"/128", "dev", interfaceName)); err != nil {
			return err
		}
	}

	delete(vms, address)
	if registered && interfaceExists && !slices.Contains(slices.Collect(maps.Values(vms)), ifindex) {
		if err := detachHook(interfaceName, "ingress"); err != nil {
			return err
		}
	}

	fmt.Printf("VM %s is removed from %s\n", canonicalAddress, interfaceName)
	return nil
}

func readLocalVMs() (map[[16]byte]uint32, error) {
	return readMapEntries[[16]byte, uint32]("local_vms")
}

func listVMs() error {
	vms, err := readLocalVMs()
	if err != nil {
		return err
	}
	type item struct {
		Address   string `json:"address"`
		Interface string `json:"interface"`
	}
	items := make([]item, 0, len(vms))
	for _, address := range sortedAddresses(vms) {
		items = append(items, item{address.String(), deviceName(vms[address.As16()])})
	}
	if listJSON {
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	for _, item := range items {
		fmt.Printf("%s\t%s\n", item.Address, item.Interface)
	}
	return nil
}

func replacePrivilegedVMs(addressTexts []string) error {
	wanted := make(map[[16]byte]uint8, len(addressTexts))
	for _, text := range addressTexts {
		address, err := parseMeshAddress(text)
		if err != nil {
			return err
		}
		if address[4]|address[5]|address[6]|address[7] != 0 {
			return fmt.Errorf("%s is not a tenant-0 address", text)
		}
		wanted[address] = 1
	}

	unlock, err := lockFile(vmLockPath, true)
	if err != nil {
		return err
	}
	defer unlock()
	current, err := readMapEntries[[16]byte, uint8]("privileged_vms")
	if err != nil {
		return err
	}
	for address := range current {
		if _, keep := wanted[address]; !keep {
			if err := deleteMapKey("privileged_vms", address); err != nil {
				return err
			}
		}
	}
	for address := range wanted {
		if err := writeMap("privileged_vms", address, uint8(1)); err != nil {
			return err
		}
	}

	fmt.Printf("Atlas WG Mesh holds %d privileged VMs\n", len(wanted))
	return nil
}

func listPrivilegedVMs() error {
	privileged, err := readMapEntries[[16]byte, uint8]("privileged_vms")
	if err != nil {
		return err
	}
	type item struct {
		Address string `json:"address"`
	}
	items := make([]item, 0, len(privileged))
	for _, address := range sortedAddresses(privileged) {
		items = append(items, item{address.String()})
	}
	if listJSON {
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	for _, item := range items {
		fmt.Println(item.Address)
	}
	return nil
}

func parsePrefix(text string) (prefixKey, error) {
	prefix, err := netip.ParsePrefix(text)
	if err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return prefixKey{}, fmt.Errorf("%q is not an IPv6 prefix", text)
	}
	if prefix.Masked() != prefix {
		return prefixKey{}, fmt.Errorf("%q has host bits set", text)
	}
	return prefixKey{uint32(prefix.Bits()), prefix.Addr().As16()}, nil
}

func prefixText(prefix prefixKey) string {
	return netip.PrefixFrom(netip.AddrFrom16(prefix.Address), int(prefix.PrefixLength)).String()
}

func sortedAddresses[Value any](entries map[[16]byte]Value) []netip.Addr {
	addresses := make([]netip.Addr, 0, len(entries))
	for address := range entries {
		addresses = append(addresses, netip.AddrFrom16(address))
	}
	slices.SortFunc(addresses, netip.Addr.Compare)
	return addresses
}
