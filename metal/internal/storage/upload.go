package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// SnapshotPartSizeBytes is the fixed multipart upload size. The controller signs
// each part against this size, so it cannot change without breaking a signature.
const SnapshotPartSizeBytes int64 = 2 << 30

// SnapshotUploadPart is one presigned destination for a part of an artifact.
type SnapshotUploadPart struct {
	PartNumber int
	URL        string
}

// SnapshotArtifactUpload contains all upload parts for one artifact.
type SnapshotArtifactUpload struct {
	Parts []SnapshotUploadPart
}

// SnapshotUploadRequest contains upload URLs for both image artifacts.
type SnapshotUploadRequest struct {
	Rootfs SnapshotArtifactUpload
	Kernel SnapshotArtifactUpload
}

// UploadedPart contains the ETag returned for one part.
type UploadedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// UploadedArtifact describes one uploaded artifact.
type UploadedArtifact struct {
	SizeBytes int64          `json:"size_bytes"`
	SHA256    string         `json:"sha256"`
	Parts     []UploadedPart `json:"parts"`
}

// SnapshotUploadResult describes both uploaded image artifacts.
type SnapshotUploadResult struct {
	Rootfs UploadedArtifact `json:"rootfs"`
	Kernel UploadedArtifact `json:"kernel"`
}

// Snapshot upload states recorded in the staging metadata.
const (
	UploadStatePending   = "pending"
	UploadStateUploading = "uploading"
	UploadStateCompleted = "completed"
	UploadStateFailed    = "failed"
)

// SnapshotUploadStatus reports the upload progress of one staged snapshot.
type SnapshotUploadStatus struct {
	ID            string
	State         string
	UploadedBytes int64
	TotalBytes    int64
	Result        SnapshotUploadResult
	Error         string
}

// snapshotUpload tracks one running upload goroutine, so a delete can cancel it
// and wait for it to stop before it removes the staging data it reads.
type snapshotUpload struct {
	cancel   context.CancelFunc
	done     chan struct{}
	uploaded atomic.Int64
}

// StartUpload begins an asynchronous artifact upload and returns at once. It is
// idempotent: a call while an upload runs, or after it completes, does nothing.
func (store *SnapshotStore) StartUpload(_ context.Context, snapshotID string, request SnapshotUploadRequest) error {
	store.lifecycleMutex.Lock()
	if store.closed {
		store.lifecycleMutex.Unlock()
		return ErrShuttingDown
	}
	store.uploadsWaitGroup.Add(1)
	rootContext := store.rootContext
	store.lifecycleMutex.Unlock()

	// Shutdown waits on this counter. Every path that does not hand the upload to
	// a goroutine must release it here.
	goroutineStarted := false
	defer func() {
		if !goroutineStarted {
			store.uploadsWaitGroup.Done()
		}
	}()

	lock := store.snapshotLock(snapshotID)
	lock.Lock()
	defer lock.Unlock()

	metadata, found, err := store.loadStagedSnapshot(snapshotID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if metadata.UploadState == UploadStateCompleted {
		return nil
	}
	if err := validateUploadParts(request.Rootfs.Parts, metadata.RootfsSizeBytes); err != nil {
		return fmt.Errorf("rootfs parts: %w", err)
	}
	if err := validateUploadParts(request.Kernel.Parts, metadata.KernelSizeBytes); err != nil {
		return fmt.Errorf("kernel parts: %w", err)
	}

	uploadContext, cancel := context.WithCancel(rootContext)
	upload := &snapshotUpload{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	store.uploadsMutex.Lock()
	if _, running := store.uploads[snapshotID]; running {
		store.uploadsMutex.Unlock()
		cancel()
		return nil
	}
	store.uploads[snapshotID] = upload
	store.uploadsMutex.Unlock()

	metadata.UploadState = UploadStateUploading
	metadata.UploadError = ""
	metadata.LastActivityAt = time.Now().UTC()
	if err := store.saveStagedSnapshot(metadata); err != nil {
		cancel()
		store.removeUpload(snapshotID, upload)
		close(upload.done)
		return err
	}

	goroutineStarted = true
	go store.runUpload(uploadContext, snapshotID, upload, metadata.RootfsSizeBytes, metadata.KernelSizeBytes, request)

	return nil
}

// runUpload sends both artifacts and records the outcome. It always releases the
// shutdown counter and signals waiters, whichever way it ends.
func (store *SnapshotStore) runUpload(ctx context.Context, snapshotID string, upload *snapshotUpload, rootfsSize, kernelSize int64, request SnapshotUploadRequest) {
	defer func() {
		store.removeUpload(snapshotID, upload)
		close(upload.done)
		store.uploadsWaitGroup.Done()
	}()

	rootfs, err := store.uploadArtifact(ctx, store.pool.stagingDevicePath(snapshotID), rootfsSize, request.Rootfs.Parts, &upload.uploaded)
	if err != nil {
		store.failUpload(ctx, snapshotID, fmt.Errorf("upload rootfs: %w", err))
		return
	}
	kernel, err := store.uploadArtifact(ctx, filepath.Join(store.snapshotDirectory(snapshotID), "vmlinux"), kernelSize, request.Kernel.Parts, &upload.uploaded)
	if err != nil {
		store.failUpload(ctx, snapshotID, fmt.Errorf("upload kernel: %w", err))
		return
	}
	store.finishUpload(snapshotID, SnapshotUploadResult{Rootfs: rootfs, Kernel: kernel})
}

// finishUpload records a completed upload and its result.
func (store *SnapshotStore) finishUpload(snapshotID string, result SnapshotUploadResult) {
	store.updateUploadMetadata(snapshotID, func(metadata *stagedSnapshotMetadata) {
		metadata.UploadState = UploadStateCompleted
		metadata.UploadError = ""
		metadata.UploadResult = &result
	})
}

// failUpload records a failure. A canceled upload stays pending instead, so a
// shutdown or a delete does not look like a real failure.
func (store *SnapshotStore) failUpload(ctx context.Context, snapshotID string, cause error) {
	// Keep canceled uploads pending for retry.
	if ctx.Err() != nil {
		return
	}
	store.updateUploadMetadata(snapshotID, func(metadata *stagedSnapshotMetadata) {
		metadata.UploadState = UploadStateFailed
		metadata.UploadError = cause.Error()
	})
}

// updateUploadMetadata applies one change to the staging record under its lock.
func (store *SnapshotStore) updateUploadMetadata(snapshotID string, apply func(*stagedSnapshotMetadata)) {
	lock := store.snapshotLock(snapshotID)
	lock.Lock()
	defer lock.Unlock()

	metadata, found, err := store.loadStagedSnapshot(snapshotID)
	if err != nil || !found {
		return
	}
	apply(&metadata)
	metadata.LastActivityAt = time.Now().UTC()
	_ = store.saveStagedSnapshot(metadata)
}

// removeUpload forgets an upload, unless a newer one already replaced it.
func (store *SnapshotStore) removeUpload(snapshotID string, upload *snapshotUpload) {
	store.uploadsMutex.Lock()
	defer store.uploadsMutex.Unlock()
	if store.uploads[snapshotID] == upload {
		delete(store.uploads, snapshotID)
	}
}

// cancelAndWaitUpload stops a running upload and blocks until its goroutine
// returns, so the caller can safely remove the staging data it was reading.
func (store *SnapshotStore) cancelAndWaitUpload(snapshotID string) {
	store.uploadsMutex.Lock()
	upload := store.uploads[snapshotID]
	store.uploadsMutex.Unlock()
	if upload == nil {
		return
	}
	upload.cancel()
	<-upload.done
}

// UploadStatus reports the current upload state of one staged snapshot. An
// upload recorded as running with no goroutine behind it did not survive a host
// restart, so it is reported as pending for the controller to start again.
func (store *SnapshotStore) UploadStatus(_ context.Context, snapshotID string) (SnapshotUploadStatus, error) {
	lock := store.snapshotLock(snapshotID)
	lock.Lock()
	defer lock.Unlock()

	metadata, found, err := store.loadStagedSnapshot(snapshotID)
	if err != nil {
		return SnapshotUploadStatus{}, err
	}
	if !found {
		return SnapshotUploadStatus{}, ErrNotFound
	}
	state := metadata.UploadState
	if state == "" {
		state = UploadStatePending
	}
	status := SnapshotUploadStatus{
		ID:         snapshotID,
		TotalBytes: metadata.RootfsSizeBytes + metadata.KernelSizeBytes,
		Error:      metadata.UploadError,
	}
	if metadata.UploadResult != nil {
		status.Result = *metadata.UploadResult
	}

	if state == UploadStateCompleted {
		status.UploadedBytes = status.TotalBytes
	} else {
		store.uploadsMutex.Lock()
		upload := store.uploads[snapshotID]
		store.uploadsMutex.Unlock()
		if upload != nil {
			status.UploadedBytes = upload.uploaded.Load()
		} else if state == UploadStateUploading {
			// The upload goroutine did not survive a host restart. Report pending
			// so the controller starts the upload again.
			state = UploadStatePending
		}
	}
	status.State = state
	return status, nil
}

// validateUploadParts checks that the parts cover the artifact exactly, are
// numbered from 1 without gaps, and carry usable URLs.
func validateUploadParts(parts []SnapshotUploadPart, sizeBytes int64) error {
	if sizeBytes <= 0 {
		return fmt.Errorf("%w: artifact size must be positive", ErrInvalidUpload)
	}
	expectedCount := int((sizeBytes + SnapshotPartSizeBytes - 1) / SnapshotPartSizeBytes)
	if len(parts) != expectedCount {
		return fmt.Errorf("%w: expected %d parts", ErrInvalidUpload, expectedCount)
	}
	for index, part := range parts {
		if part.PartNumber != index+1 {
			return fmt.Errorf("%w: part numbers must be consecutive from 1", ErrInvalidUpload)
		}
		if _, err := parseImageURL(part.URL); err != nil {
			return fmt.Errorf("%w: part %d has an invalid URL", ErrInvalidUpload, part.PartNumber)
		}
	}
	return nil
}

// uploadArtifact sends one artifact part by part and digests it as it reads, so
// the file is read once for both the upload and the checksum.
func (store *SnapshotStore) uploadArtifact(ctx context.Context, path string, sizeBytes int64, parts []SnapshotUploadPart, uploaded *atomic.Int64) (UploadedArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return UploadedArtifact{}, err
	}
	defer file.Close()

	digest := sha256.New()
	uploadedParts := make([]UploadedPart, 0, len(parts))
	for index, part := range parts {
		offset := int64(index) * SnapshotPartSizeBytes
		length := min(SnapshotPartSizeBytes, sizeBytes-offset)
		reader := io.TeeReader(io.NewSectionReader(file, offset, length), digest)

		etag, err := store.uploadPart(ctx, part, reader, length)
		if err != nil {
			return UploadedArtifact{}, err
		}
		uploaded.Add(length)
		uploadedParts = append(uploadedParts, UploadedPart{PartNumber: part.PartNumber, ETag: etag})
	}

	return UploadedArtifact{
		SizeBytes: sizeBytes,
		SHA256:    hex.EncodeToString(digest.Sum(nil)),
		Parts:     uploadedParts,
	}, nil
}

// uploadPart sends one part and returns the ETag the destination reports.
func (store *SnapshotStore) uploadPart(ctx context.Context, part SnapshotUploadPart, body io.Reader, sizeBytes int64) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, part.URL, body)
	if err != nil {
		return "", fmt.Errorf("create part %d request", part.PartNumber)
	}
	request.ContentLength = sizeBytes

	response, err := store.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("upload part %d to %s failed", part.PartNumber, redactURL(part.URL))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("upload part %d to %s returned HTTP status %d", part.PartNumber, redactURL(part.URL), response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("upload part %d to %s returned no ETag", part.PartNumber, redactURL(part.URL))
	}
	return etag, nil
}
