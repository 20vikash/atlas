package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/cilium/ebpf"
	"github.com/spf13/cobra"
)

var version = "dev"

var upgradeCommand = &cobra.Command{
	Use:   "upgrade",
	Short: "replace BPF programs with this binary's version",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return upgradeBPF(uplinkName, wireGuardName)
	},
}

func showVersion() error {
	installed, err := readInstalledHash()
	fmt.Printf("CLI version: %s\nEmbedded BPF SHA-256: %x\n", version, bpfHash())
	if err == nil {
		fmt.Printf("Installed BPF SHA-256: %x\n", installed)
	} else {
		fmt.Printf("Installed BPF SHA-256: unavailable (%v)\n", err)
	}
	return nil
}

// upgradeBPF keeps every pinned map that the new release can use and
// recreates the others. It repairs the state that a recreated map loses.
func upgradeBPF(uplinkName, wireGuardName string) error {
	unlock, err := lockVMState()
	if err != nil {
		return err
	}
	defer unlock()

	installed, err := readInstalledHash()
	if err != nil {
		return err
	}

	if installed == bpfHash() {
		fmt.Println("Atlas WG Mesh BPF is already current")
		return nil
	}

	// The pinned config layout can change between releases, so rebuild it from the interfaces.
	config, err := readHostConfig(uplinkName, wireGuardName)
	if err != nil {
		return err
	}

	// Read the VM interfaces before a recreated local_vms map loses them.
	vmInterfaces, err := virtualMachineInterfaces()
	if err != nil {
		return err
	}

	spec, err := collectionSpec()
	if err != nil {
		return err
	}

	replacements, closeMaps, err := compatibleMaps(spec)
	if err != nil {
		return err
	}
	defer closeMaps()

	collection, err := loadCollection(spec, replacements)
	if err != nil {
		return err
	}
	defer collection.Close()

	for name, bpfMap := range collection.Maps {
		if _, kept := replacements[name]; kept {
			continue
		}

		path := filepath.Join(pinDirectory, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		if err := bpfMap.Pin(path); err != nil {
			return err
		}
	}

	if _, kept := replacements["local_vms"]; !kept {
		if err := restoreLocalVirtualMachines(vmInterfaces); err != nil {
			return err
		}
	}

	// Fill the rebuilt configuration before any hook attaches.
	if err := collection.Maps["config"].Put(uint32(0), config); err != nil {
		return err
	}

	if err := collection.Maps["build_hash"].Put(uint32(0), bpfHash()); err != nil {
		return err
	}

	candidateHash := bpfHash()
	hash := hex.EncodeToString(candidateHash[:])
	release := filepath.Join(pinDirectory, "releases", hash)

	if err := os.MkdirAll(release, 0755); err != nil {
		return err
	}

	for name, program := range collection.Programs {
		if err := program.Pin(filepath.Join(release, name)); err != nil &&
			!errors.Is(err, os.ErrExist) {
			return err
		}
	}

	if err := attachUplinkHook(
		uplinkName,
		filepath.Join(release, ndpProgram),
		filepath.Join(release, ndpUnicastIngressProgram),
		filepath.Join(release, ndpUnicastEgressProgram),
	); err != nil {
		return err
	}

	if err := attachHookPath(wireGuardName, filepath.Join(release, wireguardProgram), "ingress"); err != nil {
		return err
	}

	for _, interfaceName := range vmInterfaces {
		if err := attachHookPath(interfaceName, filepath.Join(release, vmBPFProgram), "ingress"); err != nil {
			return err
		}
	}

	// A valid neighbour entry sends no solicitation, so the NDP hook cannot relearn a location until it expires.
	if _, kept := replacements["remote_vms"]; !kept {
		if err := runCommand("ip", "-6", "neigh", "flush", "dev", uplinkName, "to", meshRoutePrefix); err != nil {
			return err
		}
	}

	// Remove the top level pins of a previous install.
	for _, program := range []string{
		vmBPFProgram,
		ndpProgram,
		ndpUnicastIngressProgram,
		ndpUnicastEgressProgram,
		wireguardProgram,
	} {
		_ = os.Remove(filepath.Join(pinDirectory, program))
	}

	cleanReleases(hash, installed)

	fmt.Printf("Atlas WG Mesh BPF upgraded to %s\n", hash[:12])
	return nil
}

// restoreLocalVirtualMachines refills a recreated local_vms map from the VM routes.
func restoreLocalVirtualMachines(vmInterfaces map[[16]byte]string) error {
	for vm, interfaceName := range vmInterfaces {
		device, err := net.InterfaceByName(interfaceName)
		if err != nil {
			return err
		}

		if err := addLocalVirtualMachine(vm, uint32(device.Index)); err != nil {
			return err
		}
	}

	return nil
}

// attachUplinkHook attaches the NDP hook on the uplink from pinned program paths.
func attachUplinkHook(uplinkName, multicastPath, unicastIngressPath, unicastEgressPath string) error {
	if unicastTransportActive(uplinkName) {
		if err := attachUnicastHookPath(uplinkName, unicastIngressPath, "ingress", unicastIngressFilterPriority); err != nil {
			return err
		}

		return attachUnicastHookPath(uplinkName, unicastEgressPath, "egress", unicastEgressFilterPriority)
	}

	return attachHookPath(uplinkName, multicastPath, "ingress")
}

// virtualMachineInterfaces maps every registered VM to its host interface. It
// reads the keys with a raw value buffer sized to the map itself, so it still
// works when the new release changes the value layout of local_vms.
func virtualMachineInterfaces() (map[[16]byte]string, error) {
	vmMap, err := openMap("local_vms")
	if err != nil {
		return nil, err
	}
	defer vmMap.Close()

	value := make([]byte, vmMap.ValueSize())
	var vm [16]byte
	interfaces := make(map[[16]byte]string)

	iterator := vmMap.Iterate()
	for iterator.Next(&vm, &value) {
		address := netip.AddrFrom16(vm).String()

		output, err := commandOutput("ip", "-o", "-6", "route", "show", address+"/128")
		if err != nil {
			return nil, err
		}

		interfaceName := fieldAfter(strings.Fields(output), "dev")
		if interfaceName == "" {
			return nil, fmt.Errorf("no route for local VM %s", address)
		}

		interfaces[vm] = interfaceName
	}

	return interfaces, iterator.Err()
}

// compatibleMaps opens every pinned map that the new release can keep. The
// new release recreates an incompatible map empty.
func compatibleMaps(spec *ebpf.CollectionSpec) (map[string]*ebpf.Map, func(), error) {
	maps := make(map[string]*ebpf.Map)
	closeMaps := func() {
		for _, bpfMap := range maps {
			bpfMap.Close()
		}
	}

	for name, mapSpec := range spec.Maps {
		bpfMap, err := openMap(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			closeMaps()
			return nil, func() {}, err
		}

		if mapSpec.Compatible(bpfMap) != nil {
			bpfMap.Close()
			continue
		}

		maps[name] = bpfMap
	}

	return maps, closeMaps, nil
}

func cleanReleases(current string, previous [32]byte) {
	entries, err := os.ReadDir(filepath.Join(pinDirectory, "releases"))
	if err != nil {
		return
	}

	previousName := hex.EncodeToString(previous[:])

	for _, entry := range entries {
		if entry.Name() == current || entry.Name() == previousName {
			continue
		}

		_ = os.RemoveAll(filepath.Join(pinDirectory, "releases", entry.Name()))
	}
}
