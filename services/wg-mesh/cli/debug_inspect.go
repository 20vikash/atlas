package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/cilium/ebpf"
)

func inspectVirtualMachine(addressText string) error {
	virtualMachine, err := parseMeshAddress(addressText)
	if err != nil {
		return err
	}
	local, err := isLocalVirtualMachine(virtualMachine)
	if err != nil {
		return err
	}
	fmt.Printf("VM: %s\nlocal: %t\n", addressText, local)
	if local {
		return nil
	}
	host, found, err := remoteHostOf(virtualMachine)
	if err != nil {
		return err
	}
	if !found {
		fmt.Println("remote: not learned")
		return nil
	}
	fmt.Printf("remote host: %s\n", host)
	return inspectWireGuardHost(host)
}

func isLocalVirtualMachine(virtualMachine [16]byte) (bool, error) {
	localVMs, err := openMap("local_vms")
	if err != nil {
		return false, err
	}
	defer localVMs.Close()

	var value uint32
	err = localVMs.Lookup(virtualMachine, &value)
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return false, nil
	}
	return err == nil, err
}

// remoteHostOf reads the neighbour entry of a remote VM and maps its MAC to the owning peer through peers_by_mac.
func remoteHostOf(virtualMachine [16]byte) (netip.Addr, bool, error) {
	address := netip.AddrFrom16(virtualMachine)
	output, err := commandOutput("ip", "-o", "-6", "neigh", "show", "to", address.String())
	if err != nil {
		return netip.Addr{}, false, err
	}

	macText := fieldAfter(strings.Fields(output), "lladdr")
	if macText == "" {
		return netip.Addr{}, false, nil
	}

	mac, err := net.ParseMAC(macText)
	if err != nil || len(mac) != 6 {
		return netip.Addr{}, false, nil
	}

	peerMap, err := openMap("peers_by_mac")
	if err != nil {
		return netip.Addr{}, false, err
	}
	defer peerMap.Close()

	var host [16]byte
	key := packPeerMAC([6]byte(mac))
	if err := peerMap.Lookup(key, &host); err != nil {
		return netip.Addr{}, false, nil
	}
	return netip.AddrFrom16(host), true, nil
}

func inspectWireGuardHost(host netip.Addr) error {
	route, err := commandOutput("ip", "-6", "route", "get", host.String())
	if err != nil {
		return err
	}
	interfaceName := fieldAfter(strings.Fields(route), "dev")
	if interfaceName == "" {
		fmt.Printf("route: %s", route)
		return nil
	}
	fmt.Printf("route: %s", strings.TrimSpace(route))
	peer, err := wireGuardPeer(interfaceName, host)
	if err != nil {
		fmt.Printf("\nWireGuard peer: unavailable (%v)\n", err)
		return nil
	}
	if peer == "" {
		fmt.Println("\nWireGuard peer: no matching AllowedIPs entry")
		return nil
	}
	fmt.Printf("\nWireGuard peer: %s\n", peer)
	return nil
}

func wireGuardPeer(interfaceName string, host netip.Addr) (string, error) {
	output, err := commandOutput("wg", "show", interfaceName, "dump")
	if err != nil {
		return "", err
	}
	for lineNumber, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if lineNumber == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 5 && allowedByPeer(fields[3], host) {
			return fmt.Sprintf("public key=%s endpoint=%s handshake=%s", fields[0], fields[2], fields[4]), nil
		}
	}
	return "", nil
}

func allowedByPeer(allowedIPs string, host netip.Addr) bool {
	for _, allowedIP := range strings.Split(allowedIPs, ",") {
		prefix, err := netip.ParsePrefix(allowedIP)
		if err == nil && prefix.Contains(host) {
			return true
		}
	}
	return false
}

func fieldAfter(fields []string, fieldName string) string {
	for index, field := range fields {
		if field == fieldName && index+1 < len(fields) {
			return fields[index+1]
		}
	}
	return ""
}
