package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunInNetworkNamespaceBuildsNetnsExecCommand(t *testing.T) {
	directory := t.TempDir()
	ipPath := filepath.Join(directory, "ip")
	if err := os.WriteFile(ipPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	output, err := RunInNetworkNamespace(t.Context(), "metal-vm-1", "ip", "link", "show", "tap0")
	if err != nil {
		t.Fatal(err)
	}
	want := "netns\nexec\nmetal-vm-1\nip\nlink\nshow\ntap0\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}
