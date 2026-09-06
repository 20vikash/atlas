// Package storage manages ZFS images, virtual machine disks, and snapshots.
package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/frappe/atlas/metal/internal/vm"
)

// ZFSPool manages datasets in one ZFS pool.
type ZFSPool struct {
	name string
}

// VirtualMachineStore manages virtual machine disks.
type VirtualMachineStore struct {
	pool   *ZFSPool
	images *ImageStore
}

// ImageStore manages local image artifacts and cache policy.
type ImageStore struct {
	pool         *ZFSPool
	directory    string
	policiesFile string
	httpClient   *http.Client
	imageLocks   sync.Map
	logger       *slog.Logger
}

// SnapshotStore manages staged image snapshots.
type SnapshotStore struct {
	pool          *ZFSPool
	images        *ImageStore
	directory     string
	httpClient    *http.Client
	snapshotLocks sync.Map

	uploadsMutex     sync.Mutex
	uploads          map[string]*snapshotUpload
	lifecycleMutex   sync.Mutex
	rootContext      context.Context
	rootCancel       context.CancelFunc
	uploadsWaitGroup sync.WaitGroup
	closed           bool
	logger           *slog.Logger
}

// Stores contains the host storage services.
type Stores struct {
	Pool            *ZFSPool
	VirtualMachines *VirtualMachineStore
	Images          *ImageStore
	Snapshots       *SnapshotStore
}

// NewStores returns storage services for one ZFS pool.
func NewStores(parentContext context.Context, poolName, imagesDirectory string, logger *slog.Logger) Stores {
	rootContext, rootCancel := context.WithCancel(parentContext)
	if logger == nil {
		logger = slog.Default()
	}
	pool := &ZFSPool{name: poolName}
	baseDirectory := filepath.Dir(imagesDirectory)
	images := &ImageStore{
		pool:         pool,
		directory:    imagesDirectory,
		policiesFile: filepath.Join(baseDirectory, "image-policies.json"),
		httpClient:   newImageHTTPClient(),
		logger:       logger,
	}

	return Stores{
		Pool:            pool,
		VirtualMachines: &VirtualMachineStore{pool: pool, images: images},
		Images:          images,
		Snapshots: &SnapshotStore{
			pool:        pool,
			images:      images,
			directory:   filepath.Join(baseDirectory, "snapshots"),
			httpClient:  newImageHTTPClient(),
			uploads:     make(map[string]*snapshotUpload),
			rootContext: rootContext,
			rootCancel:  rootCancel,
			logger:      logger,
		},
	}
}

// Shutdown stops new uploads and waits for active uploads to finish.
func (store *SnapshotStore) Shutdown(shutdownContext context.Context) error {
	store.lifecycleMutex.Lock()
	if !store.closed {
		store.closed = true
		store.rootCancel()
	}
	store.lifecycleMutex.Unlock()

	finished := make(chan struct{})
	go func() {
		store.uploadsWaitGroup.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-shutdownContext.Done():
		return fmt.Errorf("wait for snapshot uploads: %w", shutdownContext.Err())
	}
}

func (store *ImageStore) imageDirectory(imageReference string) string {
	return filepath.Join(store.directory, imageReference)
}

func (store *ImageStore) kernelFile(imageReference string) string {
	return filepath.Join(store.imageDirectory(imageReference), "vmlinux")
}

func (store *ImageStore) manifestFile(imageReference string) string {
	return filepath.Join(store.imageDirectory(imageReference), "manifest.json")
}

func (store *ImageStore) imageLock(imageReference string) *sync.Mutex {
	lock, _ := store.imageLocks.LoadOrStore(imageReference, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (store *SnapshotStore) snapshotLock(snapshotID string) *sync.Mutex {
	lock, _ := store.snapshotLocks.LoadOrStore(snapshotID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (store *SnapshotStore) snapshotDirectory(snapshotID string) string {
	return filepath.Join(store.directory, snapshotID)
}

func notFoundAware(err error) error {
	if err != nil && strings.Contains(err.Error(), "does not exist") {
		return ErrNotFound
	}

	return err
}

// ErrNotFound indicates that a disk, snapshot, or image does not exist.
var ErrNotFound = errors.New("storage: not found")

// ErrInUse indicates that an image has dependent virtual machines.
var ErrInUse = errors.New("storage: in use")

// ErrImageConflict indicates that an image reference has different content.
var ErrImageConflict = errors.New("storage: image content conflict")

// ErrImageIntegrity indicates that image verification failed.
var ErrImageIntegrity = errors.New("storage: image integrity check failed")

// VirtualMachineStorageRequest identifies the files and disk for one virtual machine.
type VirtualMachineStorageRequest struct {
	VirtualMachineID string
	ImageReference   string
	Image            vm.Image
	ChrootRoot       string
	UserID           uint32
	GroupID          uint32
	DiskMiB          int
	SourceSnapshot   string
}

// BootConfiguration contains the files that Firecracker needs to boot.
type BootConfiguration struct {
	Kernel     string
	KernelArgs string
	Drives     []Drive
}

// Drive describes one Firecracker block device.
type Drive struct {
	Path     string
	ReadOnly bool
	Root     bool
}

// Usage describes disk allocation.
type Usage = vm.DiskUsage
