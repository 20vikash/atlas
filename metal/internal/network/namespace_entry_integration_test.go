//go:build integration

package network

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestInNamespaceEntersARealNamespace confirms real namespace entry and restore
// on the host. It needs root and the ip command. Run it with:
//
//	sudo -E go test -tags integration -run TestInNamespaceEntersARealNamespace ./internal/network/
func TestInNamespaceEntersARealNamespace(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a network namespace")
	}

	name := fmt.Sprintf("metal-test-%d", os.Getpid())
	runOrSkip(t, "ip", "netns", "add", name)
	t.Cleanup(func() {
		context, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(context, "ip", "netns", "del", name).Run()
	})

	path := "/run/netns/" + name
	hostNamespace, err := os.Readlink(currentNamespacePath)
	if err != nil {
		t.Fatal(err)
	}

	var insideNamespace string
	err = inNamespace(osNamespaceSyscalls{}, path, func() error {
		link, readErr := os.Readlink(currentNamespacePath)
		insideNamespace = link
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}

	if insideNamespace == hostNamespace {
		t.Errorf("inside namespace %q equals the host namespace, so entry did not happen", insideNamespace)
	}

	restored, err := os.Readlink(currentNamespacePath)
	if err != nil {
		t.Fatal(err)
	}
	if restored != hostNamespace {
		t.Errorf("namespace after restore = %q, want the host namespace %q", restored, hostNamespace)
	}
}

// runOrSkip runs a setup command and skips the test when it fails.
func runOrSkip(t *testing.T, name string, args ...string) {
	t.Helper()
	context, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(context, name, args...).CombinedOutput(); err != nil {
		t.Skipf("setup command %s failed: %v: %s", name, err, output)
	}
}
