package firecracker

import (
	"strings"
	"testing"
	"time"
)

// completeManifest is a fully populated, valid manifest fixture.
func completeManifest() memorySnapshotManifest {
	return newMemorySnapshotManifest(memorySnapshotManifest{
		VirtualMachineID:         "vm-1",
		UserID:                   100001,
		SpecificationGeneration:  4,
		RestartGeneration:        1,
		FirecrackerCompatibility: "firecracker-1.16.1",
		CreatedAt:                time.Unix(1_700_000_000, 0).UTC(),
		StateFileSizeBytes:       2048,
		MemoryFileSizeBytes:      1 << 20,
	})
}

// incompleteManifest omits the file sizes, which validation later rejects.
func incompleteManifest() memorySnapshotManifest {
	manifest := completeManifest()
	manifest.StateFileSizeBytes = 0
	manifest.MemoryFileSizeBytes = 0
	return manifest
}

func TestMemorySnapshotManifestUsesFixedFileNames(t *testing.T) {
	manifest := newMemorySnapshotManifest(memorySnapshotManifest{StateFileName: "/etc/passwd", MemoryFileName: "../escape"})
	if manifest.StateFileName != memorySnapshotStateFileName || manifest.MemoryFileName != memorySnapshotMemoryFileName {
		t.Fatalf("file names = %q, %q; want the fixed names", manifest.StateFileName, manifest.MemoryFileName)
	}
}

func TestMemorySnapshotManifestRoundTrips(t *testing.T) {
	original := completeManifest()
	data, err := encodeMemorySnapshotManifest(original)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := decodeMemorySnapshotManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("created_at = %v, want %v", decoded.CreatedAt, original.CreatedAt)
	}
	decoded.CreatedAt, original.CreatedAt = time.Time{}, time.Time{}
	if decoded != original {
		t.Errorf("decoded = %+v, want %+v", decoded, original)
	}
}

func TestDecodeMemorySnapshotManifestRejectsUnknownFields(t *testing.T) {
	data, err := encodeMemorySnapshotManifest(completeManifest())
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := strings.Replace(string(data), "{", `{"unexpected":true,`, 1)

	if _, err := decodeMemorySnapshotManifest([]byte(withUnknown)); err == nil {
		t.Fatal("want an error for an unknown field")
	}
}

func TestIncompleteManifestFixtureHasNoSizes(t *testing.T) {
	manifest := incompleteManifest()
	if manifest.StateFileSizeBytes != 0 || manifest.MemoryFileSizeBytes != 0 {
		t.Fatal("the incomplete fixture must have zero sizes")
	}
}
