package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

const (
	// recordSchemaVersion is the on-disk format both records use. A record with
	// any other version is rejected, never migrated in place.
	recordSchemaVersion = 1

	desiredFileName  = "config.json"
	observedFileName = "status.json"
)

// DesiredRecord stores the complete desired state of one virtual machine.
type DesiredRecord struct {
	SchemaVersion     int    `json:"schema_version"`
	ID                string `json:"id"`
	UserID            uint32 `json:"user_id"`
	GroupID           uint32 `json:"group_id"`
	CreateFingerprint string `json:"create_fingerprint"`
	Generation        uint64 `json:"generation"`
	RestartGeneration uint64 `json:"restart_generation"`
	// SpecificationGeneration bumps only when the compute, disk, or network shape
	// changes. It stays stable across power and warm-stop changes, so a memory
	// snapshot keyed on it survives a stop and start.
	SpecificationGeneration uint64 `json:"specification_generation,omitempty"`
	State                   State  `json:"state"`
	// WarmStop asks a stop to save a memory snapshot instead of shutting the
	// guest down. It is meaningful only when State is stopped. A later start
	// resumes from the snapshot. It defaults false, so an old record stops cold.
	WarmStop      bool          `json:"warm_stop,omitempty"`
	Specification Specification `json:"specification"`
}

// ObservedRecord stores reconciliation progress and observed state.
type ObservedRecord struct {
	SchemaVersion          int              `json:"schema_version"`
	Generation             uint64           `json:"generation"`
	RestartGeneration      uint64           `json:"restart_generation"`
	State                  State            `json:"state"`
	Phase                  string           `json:"phase,omitempty"`
	OperationID            string           `json:"operation_id,omitempty"`
	OperationStartedAt     time.Time        `json:"operation_started_at,omitempty"`
	UpdatedAt              time.Time        `json:"updated_at"`
	Error                  *OperationError  `json:"error,omitempty"`
	RuntimeCleanupComplete bool             `json:"runtime_cleanup_complete,omitempty"`
	NetworkCleanupComplete bool             `json:"network_cleanup_complete,omitempty"`
	StorageCleanupComplete bool             `json:"storage_cleanup_complete,omitempty"`
	NetworkInterface       NetworkInterface `json:"network_interface,omitempty"`
	Disk                   DiskUsage        `json:"disk,omitempty"`
	Sleep                  *SleepProgress   `json:"sleep,omitempty"`
}

// SleepProgress records the automatic sleep operation of one VM. It holds only
// safe times and generation numbers. It never holds artifact paths, which the
// Firecracker runtime derives from the VM ID.
type SleepProgress struct {
	EligibleAt            time.Time `json:"eligible_at,omitempty"`
	RequestedAt           time.Time `json:"requested_at,omitempty"`
	SnapshotGeneration    uint64    `json:"snapshot_generation,omitempty"`
	SnapshotCreatedAt     time.Time `json:"snapshot_created_at,omitempty"`
	LastNetworkActivityAt time.Time `json:"last_network_activity_at,omitempty"`
}

// validate rejects a corrupt sleep object. A published snapshot needs both a
// generation and a creation time, and a sleeping VM needs a published snapshot.
func (record ObservedRecord) validateSleep() error {
	if record.State == StateSleeping && record.Sleep == nil {
		return errors.New("sleeping record has no sleep progress")
	}
	sleep := record.Sleep
	if sleep == nil {
		return nil
	}
	if (sleep.SnapshotGeneration == 0) != sleep.SnapshotCreatedAt.IsZero() {
		return errors.New("sleep progress has a partial snapshot")
	}
	if record.State == StateSleeping && sleep.SnapshotGeneration == 0 {
		return errors.New("sleeping record has no published snapshot")
	}
	return nil
}

// OperationError stores safe and local reconciliation error details. Message is
// returned to the controller. LocalDetail stays on the host.
type OperationError struct {
	Code        string    `json:"code"`
	Message     string    `json:"message"`
	LocalDetail string    `json:"local_detail"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// completeOperation clears in-flight fields after the caller finishes an
// operation sequence. The caller sets State first when the sequence observed one.
func (record *ObservedRecord) completeOperation() {
	record.Phase = ""
	record.OperationID = ""
	record.OperationStartedAt = time.Time{}
	record.Error = nil
	record.UpdatedAt = time.Now().UTC()
}

// recordStore reads and writes the two record files of every virtual machine.
type recordStore struct {
	directory string
}

// newRecordStore returns a store rooted at the machines directory.
func newRecordStore(directory string) *recordStore {
	return &recordStore{directory: directory}
}

// validateAll rejects a machines directory that no longer makes sense, so the
// daemon fails at startup instead of reconciling from corrupt state. It reports
// but never repairs, because losing desired state is worse than refusing to run.
func (store *recordStore) validateAll() error {
	identifiers, err := store.listIDs()
	if err != nil {
		return err
	}
	for _, identifier := range identifiers {
		desired, err := store.readDesired(identifier)
		if err != nil {
			return err
		}
		observed, err := store.readObserved(identifier)
		if err != nil {
			return err
		}
		if observed.Generation > desired.Generation || observed.RestartGeneration > desired.RestartGeneration {
			return fmt.Errorf("validate %s: observed generation is ahead of desired generation", store.virtualMachineDirectory(identifier))
		}
	}
	return nil
}

// listIDs returns every reserved identifier in sorted order.
func (store *recordStore) listIDs() ([]string, error) {
	entries, err := os.ReadDir(store.directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list virtual machine records: %w", err)
	}
	identifiers := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			identifiers = append(identifiers, entry.Name())
		}
	}
	sort.Strings(identifiers)
	return identifiers, nil
}

// readDesired reads and validates one desired record.
func (store *recordStore) readDesired(identifier string) (DesiredRecord, error) {
	var record DesiredRecord
	if err := readRecord(store.desiredPath(identifier), &record); err != nil {
		return DesiredRecord{}, err
	}
	if record.SchemaVersion != recordSchemaVersion {
		return DesiredRecord{}, fmt.Errorf("read %s: unsupported schema version %d", store.desiredPath(identifier), record.SchemaVersion)
	}
	if record.ID != identifier {
		return DesiredRecord{}, fmt.Errorf("read %s: virtual machine identifier mismatch", store.desiredPath(identifier))
	}
	fingerprint, fingerprintError := hex.DecodeString(record.CreateFingerprint)
	if fingerprintError != nil || len(fingerprint) != sha256.Size || record.Generation == 0 || !IsDesiredState(record.State) {
		return DesiredRecord{}, fmt.Errorf("read %s: invalid desired record", store.desiredPath(identifier))
	}
	return record, nil
}

// readObserved reads and validates one observed record.
func (store *recordStore) readObserved(identifier string) (ObservedRecord, error) {
	var record ObservedRecord
	if err := readRecord(store.observedPath(identifier), &record); err != nil {
		return ObservedRecord{}, err
	}
	if record.SchemaVersion != recordSchemaVersion {
		return ObservedRecord{}, fmt.Errorf("read %s: unsupported schema version %d", store.observedPath(identifier), record.SchemaVersion)
	}
	if !isObservedState(record.State) || record.UpdatedAt.IsZero() {
		return ObservedRecord{}, fmt.Errorf("read %s: invalid observed record", store.observedPath(identifier))
	}
	if err := record.validateSleep(); err != nil {
		return ObservedRecord{}, fmt.Errorf("read %s: %w", store.observedPath(identifier), err)
	}
	return record, nil
}

// writeDesired stamps the schema version and replaces the desired record.
func (store *recordStore) writeDesired(record DesiredRecord) error {
	record.SchemaVersion = recordSchemaVersion
	return writeRecord(store.desiredPath(record.ID), record)
}

// writeObserved stamps the schema version and replaces the observed record.
func (store *recordStore) writeObserved(identifier string, record ObservedRecord) error {
	record.SchemaVersion = recordSchemaVersion
	return writeRecord(store.observedPath(identifier), record)
}

// remove deletes every record of one virtual machine.
func (store *recordStore) remove(identifier string) error {
	return os.RemoveAll(store.virtualMachineDirectory(identifier))
}

// virtualMachineDirectory is where one virtual machine keeps its records.
func (store *recordStore) virtualMachineDirectory(identifier string) string {
	return filepath.Join(store.directory, identifier)
}

// desiredPath is the desired record file of one virtual machine.
func (store *recordStore) desiredPath(identifier string) string {
	return filepath.Join(store.virtualMachineDirectory(identifier), desiredFileName)
}

// observedPath is the observed record file of one virtual machine.
func (store *recordStore) observedPath(identifier string) string {
	return filepath.Join(store.virtualMachineDirectory(identifier), observedFileName)
}

// readRecord decodes one record file. Unknown fields and trailing values are
// rejected, so a record written by a newer or broken build never loads silently.
func readRecord(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read %s: %w", path, ErrNotFound)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// rejectTrailingJSON reports a second JSON value after the record.
func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("trailing JSON value")
	}
	return err
}

// writeRecord publishes one record with an atomic rename.
func writeRecord(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create record directory: %w", err)
	}
	if err := platform.WriteFile(path, data, 0o640); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
