package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/spf13/cobra"
)

// Must match struct atlas_peer in bpf/state.h.
type peerEntry struct {
	IPv4        [4]byte
	PrivateIPv4 [4]byte
	MAC         [6]byte
	_           [2]byte
	WG          [16]byte
}

// wireGuardPeerFileEntry is one entry of the WireGuard peer state file.
type wireGuardPeerFileEntry struct {
	Node           string `json:"node"`
	NodeID         uint32 `json:"node_id"`
	PublicKey      string `json:"public_key"`
	Address        string `json:"address"`
	PrivateAddress string `json:"private_address"`
	MAC            string `json:"mac"`
}

var peersCommand = &cobra.Command{
	Use:   "peers",
	Short: "manage the mesh peer state",
	Args:  cobra.NoArgs,
	RunE:  showHelp,
}

var peersSyncCommand = &cobra.Command{
	Use:   "sync PEERS_JSON",
	Short: "reload the BPF peer maps from the WireGuard peer state",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, arguments []string) error {
		return syncPeers(arguments[0])
	},
}

// syncPeers reloads the peer maps from the WireGuard peer state.
func syncPeers(peersPath string) error {
	config, err := readPinnedConfig()
	if err != nil {
		return err
	}

	peers, err := readMeshPeers(peersPath, config)
	if err != nil {
		return err
	}

	if err := fillPeerList(peers); err != nil {
		return err
	}
	if err := fillPeersByMAC(peers); err != nil {
		return err
	}

	fmt.Printf("Atlas WG Mesh holds %d peers\n", len(peers))
	return nil
}

// readMeshPeers converts the WireGuard peer state into mesh peers.
func readMeshPeers(peersPath string, config hostConfig) ([]peerEntry, error) {
	contents, err := os.ReadFile(peersPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []wireGuardPeerFileEntry
	if err := json.Unmarshal(contents, &entries); err != nil {
		return nil, fmt.Errorf("read %s: %w", peersPath, err)
	}

	peers := make([]peerEntry, 0, len(entries))
	for _, entry := range entries {
		if peer, usable := meshPeer(entry, config); usable {
			peers = append(peers, peer)
		}
	}

	if len(peers) > unicastPeerLimit {
		return nil, fmt.Errorf("%s holds more than %d usable peers", peersPath, unicastPeerLimit)
	}

	return peers, nil
}

// meshPeer converts one WireGuard peer state entry into a mesh peer.
func meshPeer(entry wireGuardPeerFileEntry, config hostConfig) (peerEntry, bool) {
	var peer peerEntry

	host, _, err := net.SplitHostPort(entry.Address)
	if err != nil {
		return peer, false
	}

	address, err := netip.ParseAddr(host)
	if err != nil || !address.Is4() {
		return peer, false
	}

	mac, err := net.ParseMAC(entry.MAC)
	if err != nil || len(mac) != 6 {
		return peer, false
	}

	ipv4 := address.As4()
	copy(peer.IPv4[:], ipv4[:])
	copy(peer.MAC[:], mac)
	peer.WG = peerWireGuardAddress(config.WireGuardIPv6, entry.NodeID)

	if entry.PrivateAddress != "" {
		private, err := netip.ParseAddr(entry.PrivateAddress)
		if err != nil || !private.Is4() {
			return peer, false
		}

		privateIPv4 := private.As4()
		copy(peer.PrivateIPv4[:], privateIPv4[:])
	}

	return peer, true
}

// peerWireGuardAddress builds the peer address from the local prefix and node ID.
func peerWireGuardAddress(local [16]byte, nodeID uint32) [16]byte {
	var address [16]byte

	copy(address[:4], local[:4])
	binary.BigEndian.PutUint32(address[12:], nodeID)

	return address
}

// packPeerMAC packs a MAC into the low 6 bytes of a u64 key.
func packPeerMAC(mac [6]byte) uint64 {
	var packed [8]byte

	copy(packed[:], mac[:])

	return binary.LittleEndian.Uint64(packed[:])
}

// fillPeerList rewrites the whole peer_list map.
func fillPeerList(peers []peerEntry) error {
	peerMap, err := openMap("peer_list")
	if err != nil {
		return err
	}
	defer peerMap.Close()

	for index := uint32(0); index < unicastPeerLimit; index++ {
		var value peerEntry
		if int(index) < len(peers) {
			value = peers[index]
		}
		if err := peerMap.Put(index, value); err != nil {
			return err
		}
	}
	return nil
}

// fillPeersByMAC replaces the MAC index.
func fillPeersByMAC(peers []peerEntry) error {
	peerMap, err := openMap("peers_by_mac")
	if err != nil {
		return err
	}
	defer peerMap.Close()

	var key uint64
	var address [16]byte

	keys := make([]uint64, 0, len(peers))
	iterator := peerMap.Iterate()
	for iterator.Next(&key, &address) {
		keys = append(keys, key)
	}
	if err := iterator.Err(); err != nil {
		return err
	}

	for _, existingKey := range keys {
		if err := peerMap.Delete(existingKey); err != nil {
			return err
		}
	}

	for _, peer := range peers {
		if err := peerMap.Put(packPeerMAC(peer.MAC), peer.WG); err != nil {
			return err
		}
	}
	return nil
}

// meshPeerCount counts the peers in the peer_list map.
func meshPeerCount() (int, error) {
	peerMap, err := openMap("peer_list")
	if err != nil {
		return 0, err
	}
	defer peerMap.Close()

	count := 0
	for index := uint32(0); index < unicastPeerLimit; index++ {
		var peer peerEntry
		if err := peerMap.Lookup(index, &peer); err != nil {
			return 0, err
		}
		if peer.IPv4 == [4]byte{} {
			break
		}
		count++
	}
	return count, nil
}
