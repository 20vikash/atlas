package vm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

const (
	// migrationSchemaVersion is the on-disk format for migration records.
	migrationSchemaVersion = 1

	// migrationSubdirectory holds a VM's migration records next to its own
	// records, so one VM ID locates all of its state.
	migrationSubdirectory = "migration"

	targetFileName = "target.json"
	sourceFileName = "source.json"
	tokenFileName  = "token"
)

// MigrationStatus is the lifecycle status of one migration.
type MigrationStatus string

const (
	// MigrationRunning means the migration is in progress.
	MigrationRunning MigrationStatus = "running"
	// MigrationReady means the target holds a complete copy and can cut over.
	MigrationReady MigrationStatus = "ready"
	// MigrationCompleted means the target now owns the VM.
	MigrationCompleted MigrationStatus = "completed"
	// MigrationFailed means the migration stopped with an error.
	MigrationFailed MigrationStatus = "failed"
	// MigrationAborted means the migration was canceled and cleaned up.
	MigrationAborted MigrationStatus = "aborted"
)

// isValidMigrationStatus reports whether status names one migration status.
func isValidMigrationStatus(status MigrationStatus) bool {
	switch status {
	case MigrationRunning, MigrationReady, MigrationCompleted, MigrationFailed, MigrationAborted:
		return true
	default:
		return false
	}
}

// isTerminalStatus reports whether a status is a final result that no longer
// hides the VM or reserves capacity. A failed migration is not terminal: it
// stays locked for recovery.
func isTerminalStatus(status MigrationStatus) bool {
	return status == MigrationCompleted || status == MigrationAborted
}

// MigrationPhase is the fine-grained step of a running migration.
type MigrationPhase string

const (
	// PhasePreparing means the target reserved the VM ID and is running the handshake.
	PhasePreparing MigrationPhase = "preparing"
	// PhaseCopying means the target holds the VM config and copies disk intervals.
	PhaseCopying MigrationPhase = "copying"
	// PhaseStopping means the target stops the source and pulls the final snapshot.
	PhaseStopping MigrationPhase = "stopping"
	// PhaseStarting means the target creates its network and applies the VM state.
	PhaseStarting MigrationPhase = "starting"
	// PhaseFinishing means the target commits and destroys the source.
	PhaseFinishing MigrationPhase = "finishing"
	// PhaseRollback means the target cleans up and restores the source.
	PhaseRollback MigrationPhase = "rollback"
)

// isValidMigrationPhase reports whether phase names one migration phase.
func isValidMigrationPhase(phase MigrationPhase) bool {
	switch phase {
	case PhasePreparing, PhaseCopying, PhaseStopping, PhaseStarting, PhaseFinishing, PhaseRollback:
		return true
	default:
		return false
	}
}

// PortableConfig is the source VM state that the target can safely reconstruct.
// It never carries host paths, sockets, jail files, saved memory, or local IDs.
type PortableConfig struct {
	VirtualMachineID        string        `json:"virtual_machine_id"`
	CreateFingerprint       string        `json:"create_fingerprint"`
	Generation              uint64        `json:"generation"`
	SpecificationGeneration uint64        `json:"specification_generation"`
	RestartGeneration       uint64        `json:"restart_generation"`
	DesiredState            State         `json:"desired_state"`
	Specification           Specification `json:"specification"`
}

// IntervalProgress records the transfer of one snapshot interval on the target.
type IntervalProgress struct {
	Sequence         int       `json:"sequence"`
	StartedAt        time.Time `json:"started_at"`
	DurationSeconds  int       `json:"duration_seconds"`
	BytesTransferred int64     `json:"bytes_transferred"`
	TotalBytes       int64     `json:"total_bytes"`
	ThroughputMiBps  int       `json:"throughput_mibps,omitempty"`
	GUID             string    `json:"guid,omitempty"`
	Completed        bool      `json:"completed"`
}

// TargetMigrationRecord is the target host's durable state for one migration.
// The JWT lives in a sibling file, never in this record.
type TargetMigrationRecord struct {
	SchemaVersion       int                `json:"schema_version"`
	ID                  string             `json:"id"`
	VirtualMachineID    string             `json:"virtual_machine_id"`
	Source              string             `json:"source"`
	Status              MigrationStatus    `json:"status"`
	Phase               MigrationPhase     `json:"phase"`
	Config              *PortableConfig    `json:"config,omitempty"`
	UserID              uint32             `json:"user_id,omitempty"`
	GroupID             uint32             `json:"group_id,omitempty"`
	SourceObservedState State              `json:"source_observed_state,omitempty"`
	CopyStartedAt       time.Time          `json:"copy_started_at,omitempty"`
	ActiveSequence      int                `json:"active_sequence,omitempty"`
	FinalSequence       int                `json:"final_sequence,omitempty"`
	Intervals           []IntervalProgress `json:"intervals,omitempty"`
	TargetNetworkReady  bool               `json:"target_network_ready,omitempty"`
	TargetStateApplied  bool               `json:"target_state_applied,omitempty"`

	// Completion and recovery. FinishRequested and AbortRequested record the
	// selected terminal request. The others are cleanup checkpoints.
	FinishRequested      bool `json:"finish_requested,omitempty"`
	AbortRequested       bool `json:"abort_requested,omitempty"`
	SourceStopped        bool `json:"source_stopped,omitempty"`
	TargetRuntimeRemoved bool `json:"target_runtime_removed,omitempty"`
	TargetNetworkRemoved bool `json:"target_network_removed,omitempty"`
	TargetStorageRemoved bool `json:"target_storage_removed,omitempty"`
	SourceRestored       bool `json:"source_restored,omitempty"`
	SourceUnlocked       bool `json:"source_unlocked,omitempty"`
	SourceDestroyed      bool `json:"source_destroyed,omitempty"`

	Error      *OperationError `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt time.Time       `json:"finished_at,omitempty"`
}

// SourceMigrationRecord is the source host's durable lock for one migration. Its
// presence in the VM directory is the source lock.
type SourceMigrationRecord struct {
	SchemaVersion           int       `json:"schema_version"`
	ID                      string    `json:"id"`
	VirtualMachineID        string    `json:"virtual_machine_id"`
	Caller                  string    `json:"caller"`
	OriginalDesired         State     `json:"original_desired"`
	OriginalObserved        State     `json:"original_observed"`
	Sequence                int       `json:"sequence,omitempty"`
	AcknowledgedSequence    int       `json:"acknowledged_sequence,omitempty"`
	Stopped                 bool      `json:"stopped,omitempty"`
	NetworkRemoved          bool      `json:"network_removed,omitempty"`
	FinalSequence           int       `json:"final_sequence,omitempty"`
	TemporaryDiskLimitMiBps int       `json:"temporary_disk_limit_mibps,omitempty"`
	RollbackComplete        bool      `json:"rollback_complete,omitempty"`
	DestroyRuntimeComplete  bool      `json:"destroy_runtime_complete,omitempty"`
	DestroyStorageComplete  bool      `json:"destroy_storage_complete,omitempty"`
	LockedAt                time.Time `json:"locked_at"`
}

// migrationStore reads and writes the migration records that live under each VM
// directory. Records are keyed by VM ID, because a VM has at most one migration.
type migrationStore struct {
	machinesDirectory string
}

// newMigrationStore returns a store rooted at the machines directory.
func newMigrationStore(machinesDirectory string) *migrationStore {
	return &migrationStore{machinesDirectory: machinesDirectory}
}

// validateAll rejects corrupt migration records at startup.
func (store *migrationStore) validateAll() error {
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

// listVirtualMachineIDs returns every VM ID that holds a migration record.
func (store *migrationStore) listVirtualMachineIDs() ([]string, error) {
	entries, err := os.ReadDir(store.machinesDirectory)
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

// findTarget returns the VM ID and record for one migration ID. The scan is
// small, because a host runs few migrations at once.
func (store *migrationStore) findTarget(migrationID string) (string, TargetMigrationRecord, error) {
	virtualMachineIDs, err := store.listVirtualMachineIDs()
	if err != nil {
		return "", TargetMigrationRecord{}, err
	}
	for _, virtualMachineID := range virtualMachineIDs {
		if !store.has(store.targetPath(virtualMachineID)) {
			continue
		}
		record, err := store.readTarget(virtualMachineID)
		if err != nil {
			return "", TargetMigrationRecord{}, err
		}
		if record.ID == migrationID {
			return virtualMachineID, record, nil
		}
	}
	return "", TargetMigrationRecord{}, fmt.Errorf("find migration %s: %w", migrationID, ErrNotFound)
}

// readTarget reads and validates the target record of one VM.
func (store *migrationStore) readTarget(virtualMachineID string) (TargetMigrationRecord, error) {
	var record TargetMigrationRecord
	if err := readRecord(store.targetPath(virtualMachineID), &record); err != nil {
		return TargetMigrationRecord{}, err
	}
	if record.SchemaVersion != migrationSchemaVersion {
		return TargetMigrationRecord{}, fmt.Errorf("read %s: unsupported schema version %d", store.targetPath(virtualMachineID), record.SchemaVersion)
	}
	// A terminal record has no phase. Every other record needs a valid phase.
	validPhase := isValidMigrationPhase(record.Phase) || (record.Phase == "" && isTerminalStatus(record.Status))
	if record.VirtualMachineID != virtualMachineID || record.ID == "" ||
		!isValidMigrationStatus(record.Status) || !validPhase {
		return TargetMigrationRecord{}, fmt.Errorf("read %s: invalid target record", store.targetPath(virtualMachineID))
	}
	return record, nil
}

// readSource reads and validates the source record of one VM.
func (store *migrationStore) readSource(virtualMachineID string) (SourceMigrationRecord, error) {
	var record SourceMigrationRecord
	if err := readRecord(store.sourcePath(virtualMachineID), &record); err != nil {
		return SourceMigrationRecord{}, err
	}
	if record.SchemaVersion != migrationSchemaVersion {
		return SourceMigrationRecord{}, fmt.Errorf("read %s: unsupported schema version %d", store.sourcePath(virtualMachineID), record.SchemaVersion)
	}
	if record.VirtualMachineID != virtualMachineID || record.ID == "" || record.Caller == "" {
		return SourceMigrationRecord{}, fmt.Errorf("read %s: invalid source record", store.sourcePath(virtualMachineID))
	}
	return record, nil
}

// writeTarget stamps the schema version and replaces the target record.
func (store *migrationStore) writeTarget(record TargetMigrationRecord) error {
	record.SchemaVersion = migrationSchemaVersion
	return writeRecord(store.targetPath(record.VirtualMachineID), record)
}

// writeSource stamps the schema version and replaces the source record.
func (store *migrationStore) writeSource(record SourceMigrationRecord) error {
	record.SchemaVersion = migrationSchemaVersion
	return writeRecord(store.sourcePath(record.VirtualMachineID), record)
}

// writeToken stores the migration JWT with owner-only permission.
func (store *migrationStore) writeToken(virtualMachineID, signedToken string) error {
	if err := os.MkdirAll(store.migrationDirectory(virtualMachineID), 0o750); err != nil {
		return fmt.Errorf("create migration directory: %w", err)
	}
	if err := platform.WriteFile(store.tokenPath(virtualMachineID), []byte(signedToken), 0o600); err != nil {
		return fmt.Errorf("write migration token: %w", err)
	}
	return nil
}

// readToken returns the stored migration JWT.
func (store *migrationStore) readToken(virtualMachineID string) (string, error) {
	data, err := os.ReadFile(store.tokenPath(virtualMachineID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("read migration token: %w", ErrNotFound)
		}
		return "", fmt.Errorf("read migration token: %w", err)
	}
	return string(data), nil
}

// remove deletes every migration record of one VM. It leaves the VM's own
// records in place.
func (store *migrationStore) remove(virtualMachineID string) error {
	return os.RemoveAll(store.migrationDirectory(virtualMachineID))
}

// has reports whether one record file exists.
func (store *migrationStore) has(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (store *migrationStore) migrationDirectory(virtualMachineID string) string {
	return migrationDirectory(store.machinesDirectory, virtualMachineID)
}

func (store *migrationStore) targetPath(virtualMachineID string) string {
	return filepath.Join(store.migrationDirectory(virtualMachineID), targetFileName)
}

func (store *migrationStore) sourcePath(virtualMachineID string) string {
	return filepath.Join(store.migrationDirectory(virtualMachineID), sourceFileName)
}

func (store *migrationStore) tokenPath(virtualMachineID string) string {
	return filepath.Join(store.migrationDirectory(virtualMachineID), tokenFileName)
}

// migrationDirectory is where one VM keeps its migration records.
func migrationDirectory(machinesDirectory, virtualMachineID string) string {
	return filepath.Join(machinesDirectory, virtualMachineID, migrationSubdirectory)
}

// sourceRecordPath is the source record file of one VM.
func sourceRecordPath(machinesDirectory, virtualMachineID string) string {
	return filepath.Join(migrationDirectory(machinesDirectory, virtualMachineID), sourceFileName)
}

// targetRecordPath is the target record file of one VM.
func targetRecordPath(machinesDirectory, virtualMachineID string) string {
	return filepath.Join(migrationDirectory(machinesDirectory, virtualMachineID), targetFileName)
}
