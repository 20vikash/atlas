package storage

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/vm"
)

func TestEnsureImageRejectsDifferentContentForReference(t *testing.T) {
	imageStore := NewStores(t.Context(), "metal", t.TempDir(), nil).Images
	original := imageManifest{RootfsSHA256: strings.Repeat("a", 64), KernelSHA256: strings.Repeat("b", 64), Architecture: runtime.GOARCH}
	if err := imageStore.saveImageManifest("ubuntu", original); err != nil {
		t.Fatal(err)
	}

	err := imageStore.ensureImage(context.Background(), "ubuntu", vm.Image{
		RootfsURL:    "https://images.example/rootfs?signature=secret",
		RootfsSHA256: strings.Repeat("c", 64),
		KernelURL:    "https://images.example/kernel?signature=secret",
		KernelSHA256: original.KernelSHA256,
		Architecture: runtime.GOARCH,
	})
	if !errors.Is(err, ErrImageConflict) {
		t.Fatalf("error = %v, want ErrImageConflict", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("error contains a signed URL query value")
	}
}
