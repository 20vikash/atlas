package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/cilium/ebpf"
)

func TestStructLayoutsMatchBPF(t *testing.T) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(bpfObject))
	if err != nil {
		t.Fatal(err)
	}

	config := spec.Maps["config"]
	if config.ValueSize != uint32(binary.Size(hostConfig{})) {
		t.Fatalf("config value size %d != Go size %d", config.ValueSize, binary.Size(hostConfig{}))
	}

	peerList := spec.Maps["peer_list"]
	if peerList.ValueSize != uint32(binary.Size(peerEntry{})) {
		t.Fatalf("peer_list value size %d != Go size %d", peerList.ValueSize, binary.Size(peerEntry{}))
	}
}
