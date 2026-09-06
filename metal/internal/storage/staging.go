package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
	"github.com/google/uuid"
)

// ArtifactSize contains the exact artifact size. The controller needs the byte
// count to plan a multipart upload.
type ArtifactSize struct {
	SizeBytes int64
}

// StagedSnapshot describes a local image staging snapshot.
type StagedSnapshot struct {
	ID                     string
	SourceVirtualMachineID string
	Rootfs                 ArtifactSize
	Kernel                 ArtifactSize
}

// stagedSnapshotMetadata is the on-disk record of one staged snapshot. It
// survives a restart, so an upload can be reported and retried after one.
type stagedSnapshotMetadata struct {
	ID                     string `json:"id"`
	SourceVirtualMachineID string `json:"source_virtual_machine_id"`

	SourceSnapshot  string    `json:"source_snapshot"`
	RootfsSizeBytes int64     `json:"rootfs_size_bytes"`
	KernelSizeBytes int64     `json:"kernel_size_bytes"`
	CreatedAt       time.Time `json:"created_at"`
	LastActivityAt  time.Time `json:"last_activity_at"`

	UploadState  string                `json:"upload_state,omitempty"`
	UploadError  string                `json:"upload_error,omitempty"`
	UploadResult *SnapshotUploadResult `json:"upload_result,omitempty"`
}

// Stage creates a staged snapshot for the VM manager.
func (store *SnapshotStore) Stage(ctx context.Context, request vm.SnapshotRequest) (vm.StagedSnapshot, error) {
	snapshotID, err := uuid.NewV7()
	if err != nil {
		return vm.StagedSnapshot{}, fmt.Errorf("generate snapshot identifier: %w", err)
	}
	staged, err := store.StageSnapshot(ctx, request.VirtualMachineID, snapshotID.String(), request.ImageReference)
	if err != nil {
		return vm.StagedSnapshot{}, err
	}
	return vm.StagedSnapshot{
		ID:                     staged.ID,
		SourceVirtualMachineID: staged.SourceVirtualMachineID,
		RootfsSizeBytes:        staged.Rootfs.SizeBytes,
		KernelSizeBytes:        staged.Kernel.SizeBytes,
	}, nil
}

// StageSnapshot creates a stable root file system clone and kernel link.
func (store *SnapshotStore) StageSnapshot(ctx context.Context, virtualMachineID, snapshotID, imageReference string) (StagedSnapshot, error) {
	lock := store.snapshotLock(snapshotID)
	lock.Lock()
	defer lock.Unlock()

	metadata, found, err := store.loadStagedSnapshot(snapshotID)
	if err != nil {
		return StagedSnapshot{}, err
	}
	if found {
		if metadata.SourceVirtualMachineID != virtualMachineID {
			return StagedSnapshot{}, ErrInUse
		}
		return metadata.snapshot(), nil
	}

	return store.createStagedSnapshot(ctx, virtualMachineID, snapshotID, imageReference)
}

// createStagedSnapshot snapshots the live disk, clones it read-only, and stages
// the kernel beside it. A failure attempts to destroy the clone and snapshot, so
// a retry normally starts clean.
func (store *SnapshotStore) createStagedSnapshot(ctx context.Context, virtualMachineID, snapshotID, imageReference string) (_ StagedSnapshot, resultError error) {
	sourceSnapshot := store.pool.snapshot(virtualMachineID, snapshotID)
	stagingDataset := store.pool.stagingDataset(snapshotID)
	directory := store.snapshotDirectory(snapshotID)

	if err := os.MkdirAll(directory, 0o750); err != nil {
		return StagedSnapshot{}, err
	}
	defer func() {
		if resultError != nil {
			_ = platform.Run(context.Background(), "zfs", "destroy", "-r", stagingDataset)
			_ = platform.Run(context.Background(), "zfs", "destroy", sourceSnapshot)
			_ = os.RemoveAll(directory)
		}
	}()

	if err := platform.Run(ctx, "zfs", "snapshot", sourceSnapshot); err != nil {
		return StagedSnapshot{}, fmt.Errorf("create source disk snapshot: %w", err)
	}
	if err := platform.Run(ctx, "zfs", "clone", "-o", "readonly=on", sourceSnapshot, stagingDataset); err != nil {
		return StagedSnapshot{}, fmt.Errorf("create staging disk clone: %w", err)
	}

	rootfsSize, err := volumeSizeBytes(ctx, stagingDataset)
	if err != nil {
		return StagedSnapshot{}, fmt.Errorf("read staging disk size: %w", err)
	}
	kernelSize, err := store.stageKernel(ctx, imageReference, filepath.Join(directory, "vmlinux"))
	if err != nil {
		return StagedSnapshot{}, err
	}

	now := time.Now().UTC()
	metadata := stagedSnapshotMetadata{
		ID:                     snapshotID,
		SourceVirtualMachineID: virtualMachineID,
		SourceSnapshot:         sourceSnapshot,
		RootfsSizeBytes:        rootfsSize,
		KernelSizeBytes:        kernelSize,
		CreatedAt:              now,
		LastActivityAt:         now,
	}
	if err := store.saveStagedSnapshot(metadata); err != nil {
		return StagedSnapshot{}, fmt.Errorf("save staging metadata: %w", err)
	}

	return metadata.snapshot(), nil
}

// stageKernel places the image kernel beside the staged disk and returns its
// size. A hard link is used when the staging directory shares a file system.
func (store *SnapshotStore) stageKernel(ctx context.Context, imageReference, destination string) (int64, error) {
	source := store.images.kernelFile(imageReference)
	if err := os.Link(source, destination); err != nil {
		if err := copyReflink(ctx, source, destination); err != nil {
			return 0, fmt.Errorf("stage kernel: %w", err)
		}
	}

	information, err := os.Stat(destination)
	if err != nil {
		return 0, fmt.Errorf("read staged kernel size: %w", err)
	}
	return information.Size(), nil
}

// DeleteSnapshot cancels any running upload, waits for it to stop, and then
// removes the staging clone, the source snapshot, and the staged files.
func (store *SnapshotStore) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	store.cancelAndWaitUpload(snapshotID)

	lock := store.snapshotLock(snapshotID)
	lock.Lock()
	defer lock.Unlock()

	metadata, found, err := store.loadStagedSnapshot(snapshotID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	return store.deleteStagedSnapshot(ctx, snapshotID, metadata)
}

// deleteStagedSnapshot removes the clone, the source snapshot, and the files.
// The clone goes first, because ZFS keeps a snapshot alive while a clone exists.
func (store *SnapshotStore) deleteStagedSnapshot(
	ctx context.Context,
	snapshotID string,
	metadata stagedSnapshotMetadata,
) error {
	if err := destroyIfPresent(ctx, store.pool.stagingDataset(snapshotID)); err != nil {
		return fmt.Errorf("remove staging disk: %w", err)
	}
	if err := destroyIfPresent(ctx, metadata.SourceSnapshot); err != nil {
		return fmt.Errorf("release source disk snapshot: %w", err)
	}
	if err := os.RemoveAll(store.snapshotDirectory(snapshotID)); err != nil {
		return fmt.Errorf("remove staging files: %w", err)
	}

	return nil
}

// destroyIfPresent removes a dataset and accepts one that is already gone.
func destroyIfPresent(ctx context.Context, dataset string) error {
	err := platform.Run(ctx, "zfs", "destroy", "-r", dataset)
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		return err
	}
	return nil
}

// loadStagedSnapshot reads and validates one staging record.
func (store *SnapshotStore) loadStagedSnapshot(snapshotID string) (stagedSnapshotMetadata, bool, error) {
	data, err := os.ReadFile(filepath.Join(store.snapshotDirectory(snapshotID), "metadata.json"))
	if errors.Is(err, os.ErrNotExist) {
		return stagedSnapshotMetadata{}, false, nil
	}
	if err != nil {
		return stagedSnapshotMetadata{}, false, err
	}

	var metadata stagedSnapshotMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return stagedSnapshotMetadata{}, false, fmt.Errorf("decode staging metadata: %w", err)
	}
	if metadata.ID != snapshotID || metadata.SourceVirtualMachineID == "" || metadata.SourceSnapshot == "" || metadata.RootfsSizeBytes <= 0 || metadata.KernelSizeBytes <= 0 || metadata.CreatedAt.IsZero() || metadata.LastActivityAt.IsZero() {
		return stagedSnapshotMetadata{}, false, fmt.Errorf("staging metadata is invalid")
	}
	return metadata, true, nil
}

// saveStagedSnapshot replaces one staging record.
func (store *SnapshotStore) saveStagedSnapshot(metadata stagedSnapshotMetadata) error {
	path := filepath.Join(store.snapshotDirectory(metadata.ID), "metadata.json")
	return writeJSONFile(path, metadata, 0o640)
}

// PruneStagedSnapshots removes staging with no recent activity.
func (store *SnapshotStore) PruneStagedSnapshots(
	ctx context.Context,
	now time.Time,
	maximumIdle time.Duration,
) error {
	entries, err := os.ReadDir(store.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := store.pruneStagedSnapshot(ctx, entry.Name(), now, maximumIdle); err != nil {
			return fmt.Errorf("prune snapshot %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// pruneStagedSnapshot removes staging that has been idle past maximumIdle.
func (store *SnapshotStore) pruneStagedSnapshot(
	ctx context.Context,
	snapshotID string,
	now time.Time,
	maximumIdle time.Duration,
) error {
	lock := store.snapshotLock(snapshotID)
	lock.Lock()
	defer lock.Unlock()

	metadata, found, err := store.loadStagedSnapshot(snapshotID)
	if err != nil {
		return err
	}
	if !found || now.Sub(metadata.LastActivityAt) < maximumIdle {
		return nil
	}

	return store.deleteStagedSnapshot(ctx, snapshotID, metadata)
}

// snapshot returns the caller-facing view of one staging record.
func (metadata stagedSnapshotMetadata) snapshot() StagedSnapshot {
	return StagedSnapshot{
		ID:                     metadata.ID,
		SourceVirtualMachineID: metadata.SourceVirtualMachineID,
		Rootfs:                 ArtifactSize{SizeBytes: metadata.RootfsSizeBytes},
		Kernel:                 ArtifactSize{SizeBytes: metadata.KernelSizeBytes},
	}
}

// writeJSONFile publishes one JSON document with an atomic rename.
func writeJSONFile(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return platform.WriteFile(path, data, mode)
}
