package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cilium/ebpf"
)

const pinDirectory = "/sys/fs/bpf/atlas-wg-mesh"

// Program names match the SEC("tc") functions in bpf/.
const (
	uplinkIngressProgram = "handle_uplink_ingress"
	uplinkEgressProgram  = "handle_uplink_egress"
	vmProgram            = "handle_vm_packet"
	wireGuardProgram     = "handle_wireguard_packet"
)

// Must match PEER_LIMIT in bpf/maps.h.
const peerLimit = 256

//go:embed atlas-wg-mesh.bpf.o
var bpfObject []byte

// hostConfig matches struct config in bpf/maps.h.
type hostConfig struct {
	UplinkIfIndex uint32
	PublicIfIndex uint32
	UplinkIPv4    [4]byte
	UplinkIPv6    [16]byte
	WireGuardIPv6 [16]byte
	UplinkMAC     [6]byte
	PublicMAC     [6]byte
}

// peer matches struct peer in bpf/maps.h.
type peer struct {
	IPv4          [4]byte
	MAC           [6]byte
	_             [2]byte
	WireGuardIPv6 [16]byte
}

// prefixKey matches struct prefix_key in bpf/maps.h.
type prefixKey struct {
	PrefixLength uint32
	Address      [16]byte
}

// movedPrefix matches struct moved_prefix in bpf/maps.h. ExpiresNS uses the monotonic clock of bpf_ktime_get_ns.
type movedPrefix struct {
	VirtualMachine [16]byte
	ExpiresNS      uint64
}

// routeKey matches struct route_key in bpf/maps.h. Its prefix length counts the VM address too.
type routeKey struct {
	PrefixLength   uint32
	VirtualMachine [16]byte
	Destination    [16]byte
}

func bpfHash() [32]byte {
	return sha256.Sum256(bpfObject)
}

func releaseDirectory(hash [32]byte) string {
	return filepath.Join(pinDirectory, "releases", hex.EncodeToString(hash[:]))
}

func programPathFor(hash [32]byte, program string) string {
	return filepath.Join(releaseDirectory(hash), program)
}

func programPath(program string) (string, error) {
	hash, err := readMap[[32]byte]("build_hash", uint32(0))
	if err != nil {
		return "", fmt.Errorf("read the installed BPF: %w", err)
	}

	return programPathFor(hash, program), nil
}

type bpfCandidate struct {
	hash           [32]byte
	createdMaps    []string
	createdRelease bool
	maps           map[string]bool
}

// loadBPF loads a candidate release without changing the active release.
func loadBPF(config hostConfig) (bpfCandidate, error) {
	candidate := bpfCandidate{hash: bpfHash(), maps: make(map[string]bool)}
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(bpfObject))
	if err != nil {
		return candidate, fmt.Errorf("read the embedded BPF object: %w", err)
	}

	kept := make(map[string]*ebpf.Map)
	defer func() {
		for _, bpfMap := range kept {
			bpfMap.Close()
		}
	}()
	for name, mapSpec := range spec.Maps {
		candidate.maps[name] = true
		bpfMap, openError := openMap(name)
		if openError != nil {
			if !errors.Is(openError, os.ErrNotExist) {
				return candidate, openError
			}
			continue
		}
		if err := mapSpec.Compatible(bpfMap); err != nil {
			bpfMap.Close()
			return candidate, fmt.Errorf("map %s changed layout; reset is required: %w", name, err)
		}
		kept[name] = bpfMap
	}

	collection, err := ebpf.NewCollectionWithOptions(spec, ebpf.CollectionOptions{MapReplacements: kept})
	if err != nil {
		return candidate, fmt.Errorf("load the BPF object: %w", err)
	}
	defer collection.Close()

	for name, bpfMap := range collection.Maps {
		if kept[name] != nil {
			continue
		}

		path := filepath.Join(pinDirectory, name)
		if err := bpfMap.Pin(path); err != nil {
			candidate.cleanup()
			return candidate, err
		}
		candidate.createdMaps = append(candidate.createdMaps, name)
	}

	release := releaseDirectory(candidate.hash)
	if err := os.MkdirAll(filepath.Dir(release), 0755); err != nil {
		candidate.cleanup()
		return candidate, err
	}
	if err := os.Mkdir(release, 0755); err == nil {
		candidate.createdRelease = true
	} else if !errors.Is(err, os.ErrExist) {
		candidate.cleanup()
		return candidate, err
	}
	for name, program := range collection.Programs {
		if err := program.Pin(filepath.Join(release, name)); err != nil && !errors.Is(err, os.ErrExist) {
			candidate.cleanup()
			return candidate, err
		}
	}

	if err := collection.Maps["config"].Put(uint32(0), config); err != nil {
		candidate.cleanup()
		return candidate, err
	}

	return candidate, nil
}

func (candidate bpfCandidate) commit() error {
	return writeMap("build_hash", uint32(0), candidate.hash)
}

func (candidate bpfCandidate) cleanup() {
	for _, name := range candidate.createdMaps {
		_ = os.Remove(filepath.Join(pinDirectory, name))
	}
	if candidate.createdRelease {
		_ = os.RemoveAll(releaseDirectory(candidate.hash))
	}
}

// removeOldReleases removes the pinned programs of every other release. Attached hooks hold their own program reference.
func removeOldReleases() error {
	current := filepath.Base(releaseDirectory(bpfHash()))
	entries, err := os.ReadDir(filepath.Join(pinDirectory, "releases"))
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Name() != current {
			if err := os.RemoveAll(filepath.Join(pinDirectory, "releases", entry.Name())); err != nil {
				return err
			}
		}
	}

	return nil
}

// removeObsoleteMaps removes pins that no program in the active object uses.
func removeObsoleteMaps(active map[string]bool) error {
	entries, err := os.ReadDir(pinDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || active[entry.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(pinDirectory, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func openMap(name string) (*ebpf.Map, error) {
	bpfMap, err := ebpf.LoadPinnedMap(filepath.Join(pinDirectory, name), nil)
	if err != nil {
		return nil, fmt.Errorf("open pinned map %s: %w", name, err)
	}

	return bpfMap, nil
}

func readMap[Value any](name string, key any) (Value, error) {
	var value Value

	bpfMap, err := openMap(name)
	if err != nil {
		return value, err
	}
	defer bpfMap.Close()

	return value, bpfMap.Lookup(key, &value)
}

func writeMap(name string, key, value any) error {
	bpfMap, err := openMap(name)
	if err != nil {
		return err
	}
	defer bpfMap.Close()

	return bpfMap.Put(key, value)
}

// deleteMapKey deletes one key. A missing key is not an error.
func deleteMapKey(name string, key any) error {
	bpfMap, err := openMap(name)
	if err != nil {
		return err
	}
	defer bpfMap.Close()

	if err := bpfMap.Delete(key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}

	return nil
}

// readMapEntries returns every entry of a hash or trie map.
func readMapEntries[Key comparable, Value any](name string) (map[Key]Value, error) {
	bpfMap, err := openMap(name)
	if err != nil {
		return nil, err
	}
	defer bpfMap.Close()

	entries := make(map[Key]Value)
	var key Key
	var value Value

	iterator := bpfMap.Iterate()
	for iterator.Next(&key, &value) {
		entries[key] = value
	}

	return entries, iterator.Err()
}

// readPinnedConfig reads the pinned host configuration. It tolerates the layout of an older release, so configure can compare the uplink.
func readPinnedConfig() (hostConfig, error) {
	var config hostConfig

	raw, err := readMap[[]byte]("config", uint32(0))
	if err != nil {
		return config, err
	}

	buffer := make([]byte, binary.Size(config))
	copy(buffer, raw)

	return config, binary.Read(bytes.NewReader(buffer), binary.NativeEndian, &config)
}
