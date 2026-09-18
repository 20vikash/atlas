package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestPeerState(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wireguard-peers.json")
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

var testHostConfig = hostConfig{WireGuardIPv6: [16]byte{0xfd, 0xab, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}

func TestReadMeshPeers(t *testing.T) {
	path := writeTestPeerState(t, `[
		{"node":"server-11","node_id":11,"public_key":"key11","address":"10.20.0.11:7373","mac":"aa:bb:cc:dd:ee:11"},
		{"node":"server-12","node_id":12,"public_key":"key12","address":"10.20.0.12:7373","mac":"aa:bb:cc:dd:ee:12"}
	]`)

	peers, err := readMeshPeers(path, testHostConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}
	if peers[0].IPv4 != [4]byte{10, 20, 0, 11} {
		t.Fatalf("got IPv4 %v for the first peer", peers[0].IPv4)
	}
	if peers[0].MAC != [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x11} {
		t.Fatalf("got MAC %v for the first peer", peers[0].MAC)
	}
	if peers[0].WG != [16]byte{0xfd, 0xab, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11} {
		t.Fatalf("got WireGuard address %v for the first peer", peers[0].WG)
	}
}

func TestReadMeshPeersSkipsUnusableEntries(t *testing.T) {
	path := writeTestPeerState(t, `[
		{"node":"no-mac","node_id":21,"public_key":"k","address":"10.20.0.21:7373"},
		{"node":"bad-address","node_id":22,"public_key":"k","address":"fdab::1:7373","mac":"aa:bb:cc:dd:ee:22"},
		{"node":"no-port","node_id":23,"public_key":"k","address":"10.20.0.23","mac":"aa:bb:cc:dd:ee:23"},
		{"node":"bad-mac","node_id":24,"public_key":"k","address":"10.20.0.24:7373","mac":"not-a-mac"},
		{"node":"usable","node_id":25,"public_key":"k","address":"10.20.0.25:7373","mac":"aa:bb:cc:dd:ee:25"}
	]`)

	peers, err := readMeshPeers(path, testHostConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].IPv4 != [4]byte{10, 20, 0, 25} {
		t.Fatalf("got peers %v, want only 10.20.0.25", peers)
	}
}

func TestReadMeshPeersTreatsAbsentFileAsEmpty(t *testing.T) {
	peers, err := readMeshPeers(filepath.Join(t.TempDir(), "absent.json"), testHostConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 0 {
		t.Fatalf("got %d peers, want 0", len(peers))
	}
}

func TestReadMeshPeersRejectsOverflow(t *testing.T) {
	contents := "["
	for index := 0; index <= unicastPeerLimit; index++ {
		if index > 0 {
			contents += ","
		}
		// The entry is usable only with a MAC and an IPv4 endpoint.
		contents += `{"node":"server","node_id":` + itoa(index) + `,"public_key":"k","address":"10.0.0.1:7373","mac":"aa:bb:cc:dd:ee:01"}`
	}
	contents += "]"
	path := writeTestPeerState(t, contents)

	if _, err := readMeshPeers(path, testHostConfig); err == nil {
		t.Fatal("expected an error for more peers than the map holds")
	}
}

func TestReadMeshPeersRejectsInvalidJSON(t *testing.T) {
	path := writeTestPeerState(t, "not json")

	if _, err := readMeshPeers(path, testHostConfig); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestPeerWireGuardAddress(t *testing.T) {
	local := [16]byte{0xfd, 0xab, 0x10, 0x20, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

	address := peerWireGuardAddress(local, 0x11223344)

	want := [16]byte{0xfd, 0xab, 0x10, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0x11, 0x22, 0x33, 0x44}
	if address != want {
		t.Fatalf("got address %v, want %v", address, want)
	}
}

func TestPackPeerMAC(t *testing.T) {
	packed := packPeerMAC([6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})

	// The MAC occupies the low 6 bytes of the little-endian u64 key.
	var want uint64 = 0xffeeddccbbaa
	if packed != want {
		t.Fatalf("got key %#x, want %#x", packed, want)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
