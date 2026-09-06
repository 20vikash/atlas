package platform

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFilePublishesCompleteContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	content := []byte(`{"state":"running"}`)

	if err := WriteFile(path, content, 0o640); err != nil {
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

func TestWriteFilePublishesCompleteContentDuringConcurrentWrites(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	contents := [][]byte{
		[]byte(`{"id":"first"}`),
		[]byte(`{"id":"second","value":"longer"}`),
	}

	var writers sync.WaitGroup
	for _, content := range contents {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if err := WriteFile(path, content, 0o640); err != nil {
				t.Errorf("write: %v", err)
			}
		}()
	}
	writers.Wait()

	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(contents[0]) && string(actual) != string(contents[1]) {
		t.Fatalf("content = %q", actual)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".config.json-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}
