package migration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

const (
	// migrationSchemaVersion is the on-disk record format.
	migrationSchemaVersion = 2

	// migrationSubdirectory stores migration records beside VM records.
	migrationSubdirectory = "migration"

	targetFileName = "target.json"
	sourceFileName = "source.json"
)

// Status is a migration lifecycle status.
type Status string

const (
	// StatusRunning means migration is in progress.
	StatusRunning Status = "running"
	// StatusReady means the target can cut over.
	StatusReady Status = "ready"
	// StatusCompleted means the target owns the VM.
	StatusCompleted Status = "completed"
	// StatusFailed means migration stopped with an error.
	StatusFailed Status = "failed"
	// StatusAborted means migration was canceled and cleaned up.
	StatusAborted Status = "aborted"
)

// Phase is the fine-grained step of a running migration.
type Phase string

const (
	// PhasePreparing means the target reserved the VM ID and prepares the source.
	PhasePreparing Phase = "preparing"
	// PhaseCopying means the target holds the VM definition and copies disk intervals.
	PhaseCopying Phase = "copying"
	// PhaseStopping means the target stops the source and pulls its final snapshot.
	PhaseStopping Phase = "stopping"
	// PhaseStarting means the target creates its network and applies VM state.
	PhaseStarting Phase = "starting"
	// PhaseFinishing means the target commits and removes the source.
	PhaseFinishing Phase = "finishing"
	// PhaseRollback means the target cleans up and restores the source.
	PhaseRollback Phase = "rollback"
)

type targetState string

const (
	targetPreparing       targetState = "preparing"
	targetCopying         targetState = "copying"
	targetStopping        targetState = "stopping"
	targetCreatingNetwork targetState = "creating_network"
	targetApplyingState   targetState = "applying_state"
	targetReady           targetState = "ready"
	targetFinishing       targetState = "finishing"
	targetRemovingRuntime targetState = "removing_runtime"
	targetRemovingStorage targetState = "removing_storage"
	targetRestoringSource targetState = "restoring_source"
	targetUnlockingSource targetState = "unlocking_source"
	targetCompleted       targetState = "completed"
	targetFailed          targetState = "failed"
	targetAborted         targetState = "aborted"
)

func (state targetState) valid() bool {
	switch state {
	case targetPreparing, targetCopying, targetStopping, targetCreatingNetwork, targetApplyingState,
		targetReady, targetFinishing, targetRemovingRuntime, targetRemovingStorage,
		targetRestoringSource, targetUnlockingSource, targetCompleted, targetFailed, targetAborted:
		return true
	default:
		return false
	}
}

func (state targetState) terminal() bool {
	return state == targetCompleted || state == targetAborted
}

func (state targetState) status() Status {
	switch state {
	case targetReady:
		return StatusReady
	case targetCompleted:
		return StatusCompleted
	case targetFailed:
		return StatusFailed
	case targetAborted:
		return StatusAborted
	default:
		return StatusRunning
	}
}

func (state targetState) phase() Phase {
	switch state {
	case targetPreparing:
		return PhasePreparing
	case targetCopying:
		return PhaseCopying
	case targetStopping:
		return PhaseStopping
	case targetCreatingNetwork, targetApplyingState:
		return PhaseStarting
	case targetFinishing:
		return PhaseFinishing
	case targetRemovingRuntime, targetRemovingStorage, targetRestoringSource, targetUnlockingSource:
		return PhaseRollback
	default:
		return ""
	}
}

// VirtualMachineDefinition contains the VM state that a target can reconstruct.
type VirtualMachineDefinition struct {
	VirtualMachineID        string           `json:"virtual_machine_id"`
	CreateFingerprint       string           `json:"create_fingerprint"`
	Generation              uint64           `json:"generation"`
	SpecificationGeneration uint64           `json:"specification_generation"`
	RestartGeneration       uint64           `json:"restart_generation"`
	DesiredState            vm.State         `json:"desired_state"`
	Specification           vm.Specification `json:"specification"`
}

// TransferProgress records the transfer of one snapshot interval on the target.
type TransferProgress struct {
	Sequence         int       `json:"sequence"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	DurationSeconds  int       `json:"duration_seconds"`
	BytesTransferred int64     `json:"bytes_transferred"`
	TotalBytes       int64     `json:"total_bytes"`
	ThroughputMiBps  int       `json:"throughput_mibps,omitempty"`
	GUID             string    `json:"guid,omitempty"`
	Completed        bool      `json:"completed"`
}

// TargetProgress is the controller-facing state of a target migration.
type TargetProgress struct {
	ID               string
	VirtualMachineID string
	Status           Status
	Phase            Phase
	Intervals        []TransferProgress
	Error            *vm.OperationError
}

type targetRecord struct {
	SchemaVersion       int                       `json:"schema_version"`
	ID                  string                    `json:"id"`
	VirtualMachineID    string                    `json:"virtual_machine_id"`
	Source              string                    `json:"source"`
	State               targetState               `json:"state"`
	Definition          *VirtualMachineDefinition `json:"config,omitempty"`
	SourceObservedState vm.State                  `json:"source_observed_state,omitempty"`
	CopyStartedAt       time.Time                 `json:"copy_started_at,omitempty"`
	Intervals           []TransferProgress        `json:"intervals,omitempty"`
	SourceStopped       bool                      `json:"source_stopped,omitempty"`

	Error      *vm.OperationError `json:"error,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	FinishedAt time.Time          `json:"finished_at,omitempty"`
	// LastControlAt lets an abandoned target release its reservation and data.
	LastControlAt time.Time `json:"last_control_at,omitempty"`
}

func (record targetRecord) progress() TargetProgress {
	return TargetProgress{
		ID:               record.ID,
		VirtualMachineID: record.VirtualMachineID,
		Status:           record.State.status(),
		Phase:            record.State.phase(),
		Intervals:        record.Intervals,
		Error:            record.Error,
	}
}

type sourceState string

const (
	sourceLocked         sourceState = "locked"
	sourceStopped        sourceState = "stopped"
	sourceDetached       sourceState = "detached"
	sourceRestored       sourceState = "restored"
	sourceRuntimeRemoved sourceState = "runtime_removed"
	sourceStorageRemoved sourceState = "storage_removed"
	sourceExpired        sourceState = "expired"
)

func isValidSourceState(state sourceState) bool {
	switch state {
	case sourceLocked, sourceStopped, sourceDetached, sourceRestored, sourceRuntimeRemoved, sourceStorageRemoved, sourceExpired:
		return true
	default:
		return false
	}
}

type sourceRecord struct {
	SchemaVersion           int         `json:"schema_version"`
	ID                      string      `json:"id"`
	VirtualMachineID        string      `json:"virtual_machine_id"`
	OriginalDesired         vm.State    `json:"original_desired"`
	State                   sourceState `json:"state"`
	Sequence                int         `json:"sequence,omitempty"`
	AcknowledgedSequence    int         `json:"acknowledged_sequence,omitempty"`
	FinalSequence           int         `json:"final_sequence,omitempty"`
	TemporaryDiskLimitMiBps int         `json:"temporary_disk_limit_mibps,omitempty"`
	// LastContactAt lets an idle source lock expire.
	LastContactAt time.Time `json:"last_contact_at"`
}

type migrationStore struct {
	recordsDirectory string
}

func newMigrationStore(recordsDirectory string) *migrationStore {
	return &migrationStore{recordsDirectory: recordsDirectory}
}

func (store *migrationStore) validate() error {
	virtualMachineIDs, err := store.listVirtualMachineIDs()
	if err != nil {
		return err
	}
	for _, virtualMachineID := range virtualMachineIDs {
		if store.has(store.targetPath(virtualMachineID)) {
			if _, err := store.readTarget(virtualMachineID); err != nil {
				return err
			}
		}
		if store.has(store.sourcePath(virtualMachineID)) {
			if _, err := store.readSource(virtualMachineID); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeMigrationRecord(path string, value any) error {
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

func readMigrationRecord(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read %s: %w", path, vm.ErrNotFound)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s: trailing JSON data", path)
	}
	return nil
}

func (store *migrationStore) listVirtualMachineIDs() ([]string, error) {
	entries, err := os.ReadDir(store.recordsDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list migration records: %w", err)
	}
	virtualMachineIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if store.has(store.targetPath(entry.Name())) || store.has(store.sourcePath(entry.Name())) {
			virtualMachineIDs = append(virtualMachineIDs, entry.Name())
		}
	}
	sort.Strings(virtualMachineIDs)
	return virtualMachineIDs, nil
}

func (store *migrationStore) findTarget(migrationID string) (string, targetRecord, error) {
	virtualMachineIDs, err := store.listVirtualMachineIDs()
	if err != nil {
		return "", targetRecord{}, err
	}
	for _, virtualMachineID := range virtualMachineIDs {
		if !store.has(store.targetPath(virtualMachineID)) {
			continue
		}
		record, err := store.readTarget(virtualMachineID)
		if err != nil {
			return "", targetRecord{}, err
		}
		if record.ID == migrationID {
			return virtualMachineID, record, nil
		}
	}
	return "", targetRecord{}, fmt.Errorf("find migration %s: %w", migrationID, vm.ErrNotFound)
}

func (store *migrationStore) readTarget(virtualMachineID string) (targetRecord, error) {
	var record targetRecord
	if err := readMigrationRecord(store.targetPath(virtualMachineID), &record); err != nil {
		return targetRecord{}, err
	}
	if record.SchemaVersion != migrationSchemaVersion {
		return targetRecord{}, fmt.Errorf("read %s: unsupported schema version %d", store.targetPath(virtualMachineID), record.SchemaVersion)
	}
	if record.VirtualMachineID != virtualMachineID || record.ID == "" || !record.State.valid() {
		return targetRecord{}, fmt.Errorf("read %s: invalid target record", store.targetPath(virtualMachineID))
	}
	return record, nil
}

func terminalTargetRecord(record targetRecord, state targetState, finishedAt time.Time) targetRecord {
	return targetRecord{
		SchemaVersion:    migrationSchemaVersion,
		ID:               record.ID,
		VirtualMachineID: record.VirtualMachineID,
		State:            state,
		CreatedAt:        record.CreatedAt,
		FinishedAt:       finishedAt,
	}
}

func (store *migrationStore) readSource(virtualMachineID string) (sourceRecord, error) {
	var record sourceRecord
	if err := readMigrationRecord(store.sourcePath(virtualMachineID), &record); err != nil {
		return sourceRecord{}, err
	}
	if record.SchemaVersion != migrationSchemaVersion {
		return sourceRecord{}, fmt.Errorf("read %s: unsupported schema version %d", store.sourcePath(virtualMachineID), record.SchemaVersion)
	}
	if record.VirtualMachineID != virtualMachineID || record.ID == "" || !isValidSourceState(record.State) {
		return sourceRecord{}, fmt.Errorf("read %s: invalid source record", store.sourcePath(virtualMachineID))
	}
	return record, nil
}

func (store *migrationStore) writeTarget(record targetRecord) error {
	record.SchemaVersion = migrationSchemaVersion
	return writeMigrationRecord(store.targetPath(record.VirtualMachineID), record)
}

func (store *migrationStore) writeSource(record sourceRecord) error {
	record.SchemaVersion = migrationSchemaVersion
	return writeMigrationRecord(store.sourcePath(record.VirtualMachineID), record)
}

func (store *migrationStore) remove(virtualMachineID string) error {
	return os.RemoveAll(store.migrationDirectory(virtualMachineID))
}

func (store *migrationStore) has(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

func (store *migrationStore) migrationDirectory(virtualMachineID string) string {
	return migrationDirectory(store.recordsDirectory, virtualMachineID)
}

func (store *migrationStore) targetPath(virtualMachineID string) string {
	return filepath.Join(store.migrationDirectory(virtualMachineID), targetFileName)
}

func (store *migrationStore) sourcePath(virtualMachineID string) string {
	return filepath.Join(store.migrationDirectory(virtualMachineID), sourceFileName)
}

func migrationDirectory(recordsDirectory, virtualMachineID string) string {
	return filepath.Join(recordsDirectory, virtualMachineID, migrationSubdirectory)
}

func sourceRecordPath(recordsDirectory, virtualMachineID string) string {
	return filepath.Join(migrationDirectory(recordsDirectory, virtualMachineID), sourceFileName)
}
