package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

func (store *ImageStore) deleteImage(ctx context.Context, imageReference string) error {
	lock := store.imageLock(imageReference)
	lock.Lock()
	defer lock.Unlock()

	if err := store.removeWarmImages(ctx, imageReference); err != nil {
		return err
	}
	err := platform.Run(ctx, "zfs", "destroy", "-r", store.pool.baseDataset(imageReference))
	switch {
	case err == nil:
	case strings.Contains(err.Error(), "does not exist"):
		return ErrNotFound
	case strings.Contains(err.Error(), "dependent clone"):
		return ErrInUse
	default:
		return err
	}

	if err := os.RemoveAll(store.imageDirectory(imageReference)); err != nil {
		return err
	}
	return nil
}

func (store *ImageStore) removeIncompleteImage(ctx context.Context, imageReference string) {
	_ = platform.Run(ctx, "zfs", "destroy", "-r", store.pool.baseDataset(imageReference))
	_ = os.RemoveAll(store.imageDirectory(imageReference))
}

func ignoreNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

type imageManifest struct {
	RootfsSHA256 string `json:"rootfs_sha256,omitempty"`
	KernelSHA256 string `json:"kernel_sha256,omitempty"`
	Architecture string `json:"architecture"`
	Local        bool   `json:"local,omitempty"`
}

func (store *ImageStore) ensureImage(ctx context.Context, imageReference string, image vm.Image) error {
	lock := store.imageLock(imageReference)
	lock.Lock()
	defer lock.Unlock()

	storedManifest, found, err := store.loadImageManifest(imageReference)
	if err != nil {
		return err
	}
	if found && storedManifest.Local {
		if hasImageSource(image) {
			return fmt.Errorf("%w: image reference %q identifies a local image", ErrImageConflict, imageReference)
		}
		complete, err := store.localImageArtifactsExist(ctx, imageReference)
		if err != nil {
			return err
		}
		if !complete {
			return fmt.Errorf("%w: local image %q is incomplete", ErrImageIntegrity, imageReference)
		}
		return nil
	}

	manifest, err := manifestForImage(image)
	if err != nil {
		return err
	}
	if manifest.Architecture != runtime.GOARCH {
		return fmt.Errorf("%w: image architecture does not match the host", ErrImageIntegrity)
	}
	if found && storedManifest != manifest {
		return fmt.Errorf("%w: image reference %q already identifies different content", ErrImageConflict, imageReference)
	}
	artifactsExist, err := store.imageArtifactsExist(ctx, imageReference)
	if err != nil {
		return err
	}
	if !found && artifactsExist {
		return fmt.Errorf("%w: image reference %q has no manifest", ErrImageConflict, imageReference)
	}

	completed := found
	if !found {
		defer func() {
			if completed {
				return
			}
			_ = os.RemoveAll(store.imageDirectory(imageReference))
			_ = platform.Run(context.Background(), "zfs", "destroy", "-r", store.pool.baseDataset(imageReference))
		}()
	}

	if err := store.ensureKernel(ctx, imageReference, image.KernelURL, manifest.KernelSHA256); err != nil {
		return err
	}
	exists, err := datasetExists(ctx, store.pool.baseDataset(imageReference))
	if err != nil {
		return err
	}
	if !exists {
		if err := store.importRootFileSystem(ctx, imageReference, image.RootfsURL, manifest.RootfsSHA256); err != nil {
			return err
		}
	}
	if !found {
		if err := store.saveImageManifest(imageReference, manifest); err != nil {
			return fmt.Errorf("save image manifest: %w", err)
		}
		completed = true
	}
	return nil
}

func hasImageSource(image vm.Image) bool {
	return image.RootfsURL != "" || image.KernelURL != "" || image.RootfsSHA256 != "" ||
		image.KernelSHA256 != "" || image.Architecture != ""
}

func manifestForImage(image vm.Image) (imageManifest, error) {
	if image.RootfsURL == "" || image.KernelURL == "" {
		return imageManifest{}, fmt.Errorf("%w: image and kernel URLs are required", ErrImageIntegrity)
	}
	manifest := imageManifest{
		RootfsSHA256: strings.ToLower(image.RootfsSHA256),
		KernelSHA256: strings.ToLower(image.KernelSHA256),
		Architecture: image.Architecture,
	}
	if !validSHA256(manifest.RootfsSHA256) || !validSHA256(manifest.KernelSHA256) {
		return imageManifest{}, fmt.Errorf("%w: rootfs and kernel SHA-256 digests are required", ErrImageIntegrity)
	}
	if manifest.Architecture == "" {
		return imageManifest{}, fmt.Errorf("%w: image architecture is required", ErrImageIntegrity)
	}
	return manifest, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (store *ImageStore) imageArtifactsExist(ctx context.Context, imageReference string) (bool, error) {
	exists, err := datasetExists(ctx, store.pool.baseDataset(imageReference))
	if err != nil || exists {
		return exists, err
	}

	_, err = os.Stat(store.kernelFile(imageReference))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (store *ImageStore) localImageArtifactsExist(ctx context.Context, imageReference string) (bool, error) {
	exists, err := datasetExists(ctx, store.pool.baseDataset(imageReference))
	if err != nil || !exists {
		return false, err
	}

	for _, path := range []string{
		store.kernelFile(imageReference),
		filepath.Join(store.imageDirectory(imageReference), "state"),
		filepath.Join(store.imageDirectory(imageReference), "mem"),
	} {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

func (store *ImageStore) loadImageManifest(imageReference string) (imageManifest, bool, error) {
	data, err := os.ReadFile(store.manifestFile(imageReference))
	if errors.Is(err, os.ErrNotExist) {
		return imageManifest{}, false, nil
	}
	if err != nil {
		return imageManifest{}, false, fmt.Errorf("read image manifest: %w", err)
	}

	var manifest imageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return imageManifest{}, false, fmt.Errorf("decode image manifest: %w", err)
	}
	if manifest.Architecture == "" {
		return imageManifest{}, false, fmt.Errorf("%w: stored image manifest is invalid", ErrImageIntegrity)
	}
	if !manifest.Local && (!validSHA256(manifest.RootfsSHA256) || !validSHA256(manifest.KernelSHA256)) {
		return imageManifest{}, false, fmt.Errorf("%w: stored image manifest is invalid", ErrImageIntegrity)
	}
	return manifest, true, nil
}

func (store *ImageStore) saveImageManifest(imageReference string, manifest imageManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return platform.WriteFile(store.manifestFile(imageReference), data, 0o644)
}

func (store *ImageStore) ensureKernel(ctx context.Context, imageReference, kernelURL, expectedDigest string) error {
	target := store.kernelFile(imageReference)
	if _, err := os.Stat(target); err == nil {
		if err := verifyFileSHA256(target, expectedDigest); err != nil {
			return fmt.Errorf("%w: stored kernel verification failed", ErrImageIntegrity)
		}
		return os.Chmod(target, 0o644)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	kernel, err := download(ctx, store.httpClient, store.directory, kernelURL, expectedDigest, store.logger)
	if err != nil {
		return fmt.Errorf("download kernel: %w", err)
	}
	defer os.Remove(kernel)
	if err := os.MkdirAll(store.imageDirectory(imageReference), 0o755); err != nil {
		return err
	}
	if err := os.Rename(kernel, target); err != nil {
		return err
	}
	return os.Chmod(target, 0o644)
}

func (store *ImageStore) importRootFileSystem(ctx context.Context, imageReference, rootfsURL, expectedDigest string) error {
	rootfs, err := download(ctx, store.httpClient, store.directory, rootfsURL, expectedDigest, store.logger)
	if err != nil {
		return fmt.Errorf("download root file system: %w", err)
	}
	defer os.Remove(rootfs)

	info, err := os.Stat(rootfs)
	if err != nil {
		return err
	}
	sizeMiB := info.Size()>>20 + 64
	if err := platform.Run(ctx, "zfs", "create", "-V", fmt.Sprintf("%dM", sizeMiB), "-o", "volblocksize=16k", store.pool.baseDataset(imageReference)); err != nil {
		return err
	}
	rollback := func() {
		_ = platform.Run(context.Background(), "zfs", "destroy", "-r", store.pool.baseDataset(imageReference))
	}
	if _, err := waitForBlockDevice(store.baseImageDevicePath(imageReference)); err != nil {
		rollback()
		return err
	}
	if err := platform.Run(ctx, "dd", "if="+rootfs, "of="+store.baseImageDevicePath(imageReference), "bs=4M", "conv=sparse,fsync", "status=none"); err != nil {
		rollback()
		return err
	}
	if err := platform.Run(ctx, "zfs", "snapshot", store.pool.baseSnapshot(imageReference)); err != nil {
		rollback()
		return err
	}
	return nil
}

func (store *ImageStore) baseImageDevicePath(imageReference string) string {
	return "/dev/zvol/" + store.pool.baseDataset(imageReference)
}

func newImageHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: transport, Timeout: downloadTimeout}
}

// SetImagePolicies atomically records the complete desired image policy set.
func (store *ImageStore) SetImagePolicies(ctx context.Context, images []vm.Image) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		if _, found := seen[image.Name]; found {
			return fmt.Errorf("duplicate image reference %q", image.Name)
		}
		seen[image.Name] = struct{}{}
	}
	return writeJSONFile(store.policiesFile, images, 0o600)
}

// ImagePolicies returns the desired image policy set.
func (store *ImageStore) ImagePolicies(ctx context.Context) ([]vm.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(store.policiesFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var images []vm.Image
	if err := json.Unmarshal(data, &images); err != nil {
		return nil, fmt.Errorf("decode image policies: %w", err)
	}
	return images, nil
}

// EnsureImage downloads and verifies one compatible image.
func (store *ImageStore) EnsureImage(ctx context.Context, image vm.Image) error {
	if image.Architecture != runtime.GOARCH {
		return nil
	}
	return store.ensureImage(ctx, image.Name, image)
}

// RecordImageUse records a successful virtual machine start.
func (store *ImageStore) RecordImageUse(imageReference string, now time.Time) error {
	path := filepath.Join(store.imageDirectory(imageReference), "last-used")
	return writeJSONFile(path, now.UTC(), 0o640)
}

// PruneImages removes idle images that are not retained by policy.
func (store *ImageStore) PruneImages(ctx context.Context, policies []vm.Image, now time.Time, maximumIdle time.Duration) error {
	retained := make(map[string]bool, len(policies))
	for _, policy := range policies {
		retained[policy.Name] = policy.CacheImage && policy.Architecture == runtime.GOARCH
	}

	entries, err := os.ReadDir(store.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || retained[entry.Name()] {
			continue
		}
		if err := store.pruneImage(ctx, entry.Name(), now, maximumIdle); err != nil {
			return err
		}
	}
	return nil
}

func (store *ImageStore) pruneImage(ctx context.Context, imageReference string, now time.Time, maximumIdle time.Duration) error {
	lastUsed, err := store.imageLastUsed(imageReference)
	if err != nil {
		return err
	}
	if now.Sub(lastUsed) < maximumIdle {
		return nil
	}

	err = store.deleteImage(ctx, imageReference)
	if errors.Is(err, ErrInUse) || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (store *ImageStore) imageLastUsed(imageReference string) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(store.imageDirectory(imageReference), "last-used"))
	if err == nil {
		var lastUsed time.Time
		if json.Unmarshal(data, &lastUsed) == nil && !lastUsed.IsZero() {
			return lastUsed, nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, err
	}

	information, err := os.Stat(store.manifestFile(imageReference))
	if errors.Is(err, os.ErrNotExist) {
		// Use directory age for incomplete images.
		directory, directoryErr := os.Stat(store.imageDirectory(imageReference))
		if directoryErr != nil {
			return time.Time{}, nil
		}
		return directory.ModTime(), nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return information.ModTime(), nil
}
