package firecracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// snapshotManifest describes one published snapshot.
type snapshotManifest struct {
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

// newSnapshotManifest fills the fixed artifact file names.
func newSnapshotManifest(base snapshotManifest) snapshotManifest {
	base.StateFileName = snapshotStateFileName
	base.MemoryFileName = snapshotMemoryFileName
	return base
}

// encode renders the manifest.
func encodeSnapshotManifest(manifest snapshotManifest) ([]byte, error) {
	return json.MarshalIndent(manifest, "", "  ")
}

// decodeSnapshotManifest decodes a manifest and rejects unknown or trailing data.
func decodeSnapshotManifest(data []byte) (snapshotManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var manifest snapshotManifest
	if err := decoder.Decode(&manifest); err != nil {
		return snapshotManifest{}, fmt.Errorf("decode snapshot manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return snapshotManifest{}, fmt.Errorf("decode snapshot manifest: unexpected trailing data")
	}
	return manifest, nil
}
