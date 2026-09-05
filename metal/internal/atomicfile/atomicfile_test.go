package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWritePublishesCompleteContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	content := []byte(`{"state":"running"}`)

	if err := Write(path, content, 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content: got %q, want %q", got, content)
	}
}
