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

const recordSchemaVersion = 1

// DesiredRecord stores the complete desired state of one virtual machine.
type DesiredRecord struct {
	SchemaVersion     int           `json:"schema_version"`
	ID                string        `json:"id"`
	UserID            uint32        `json:"user_id"`
	GroupID           uint32        `json:"group_id"`
	CreateFingerprint string        `json:"create_fingerprint"`
	Generation        uint64        `json:"generation"`
	RestartGeneration uint64        `json:"restart_generation"`
	State             State         `json:"state"`
	Specification     Specification `json:"specification"`
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
}

// OperationError stores safe and local reconciliation error details.
type OperationError struct {
	Code        string    `json:"code"`
	Message     string    `json:"message"`
	LocalDetail string    `json:"local_detail"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type machineStore struct {
	directory string
}

func newMachineStore(directory string) *machineStore {
	return &machineStore{directory: directory}
}

func (store *machineStore) validateAll() error {
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
			return fmt.Errorf("validate %s: observed generation is ahead of desired generation", store.machineDirectory(identifier))
		}
	}
	return nil
}

func (store *machineStore) listIDs() ([]string, error) {
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

func (store *machineStore) readDesired(identifier string) (DesiredRecord, error) {
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

func (store *machineStore) readObserved(identifier string) (ObservedRecord, error) {
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
	return record, nil
}

func (store *machineStore) writeDesired(record DesiredRecord) error {
	record.SchemaVersion = recordSchemaVersion
	return writeRecord(store.desiredPath(record.ID), record)
}

func (store *machineStore) writeObserved(identifier string, record ObservedRecord) error {
	record.SchemaVersion = recordSchemaVersion
	return writeRecord(store.observedPath(identifier), record)
}

func (store *machineStore) remove(identifier string) error {
	return os.RemoveAll(store.machineDirectory(identifier))
}

func (store *machineStore) machineDirectory(identifier string) string {
	return filepath.Join(store.directory, identifier)
}

func (store *machineStore) desiredPath(identifier string) string {
	return filepath.Join(store.machineDirectory(identifier), "config.json")
}

func (store *machineStore) observedPath(identifier string) string {
	return filepath.Join(store.machineDirectory(identifier), "status.json")
}

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
