package firecracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// memorySnapshotManifest describes one published memory snapshot.
type memorySnapshotManifest struct {
	VirtualMachineID         string    `json:"virtual_machine_id"`
	UserID                   uint32    `json:"user_id"`
	SpecificationGeneration  uint64    `json:"specification_generation"`
	RestartGeneration        uint64    `json:"restart_generation"`
	FirecrackerCompatibility string    `json:"firecracker_compatibility"`
	CreatedAt                time.Time `json:"created_at"`
	StateFileName            string    `json:"state_file_name"`
	MemoryFileName           string    `json:"memory_file_name"`
	StateFileSizeBytes       int64     `json:"state_file_size_bytes"`
	MemoryFileSizeBytes      int64     `json:"memory_file_size_bytes"`
}

// newMemorySnapshotManifest fills the fixed artifact file names.
func newMemorySnapshotManifest(base memorySnapshotManifest) memorySnapshotManifest {
	base.StateFileName = memorySnapshotStateFileName
	base.MemoryFileName = memorySnapshotMemoryFileName
	return base
}

// encodeMemorySnapshotManifest renders the manifest as indented JSON.
func encodeMemorySnapshotManifest(manifest memorySnapshotManifest) ([]byte, error) {
	return json.MarshalIndent(manifest, "", "  ")
}

// decodeMemorySnapshotManifest decodes a manifest and rejects unknown or trailing data.
func decodeMemorySnapshotManifest(data []byte) (memorySnapshotManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var manifest memorySnapshotManifest
	if err := decoder.Decode(&manifest); err != nil {
		return memorySnapshotManifest{}, fmt.Errorf("decode snapshot manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return memorySnapshotManifest{}, fmt.Errorf("decode snapshot manifest: unexpected trailing data")
	}
	return manifest, nil
}
