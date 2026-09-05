// Package vm owns virtual machine state and reconciliation.
package vm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/frappe/atlas/metal/internal/idalloc"
	"github.com/google/uuid"
)

// ManagerConfig contains persistent VM manager settings.
type ManagerConfig struct {
	MachinesDirectory string
	UserIDRange       idalloc.Range
}

// ManagerDependencies contains the host services used by Manager.
type ManagerDependencies struct {
	Runtime   Runtime
	Network   Network
	Storage   Storage
	Snapshots Snapshots
}

// Manager owns virtual machine desired state and reconciliation.
type Manager struct {
	configuration        ManagerConfig
	store                *machineStore
	runtime              Runtime
	network              Network
	storage              Storage
	snapshots            Snapshots
	operationLocks       keyedLocks
	allocationMutex      sync.Mutex
	temporaryUserIDs     map[uint32]bool
	temporaryIdentifiers map[string]bool
}

// NewManager validates all records and returns one host VM manager.
func NewManager(configuration ManagerConfig, dependencies ManagerDependencies) (*Manager, error) {
	if configuration.MachinesDirectory == "" {
		return nil, fmt.Errorf("VM machines directory is required")
	}
	if configuration.UserIDRange == (idalloc.Range{}) {
		configuration.UserIDRange = idalloc.DefaultRange
	}
	if dependencies.Runtime == nil || dependencies.Network == nil || dependencies.Storage == nil || dependencies.Snapshots == nil {
		return nil, fmt.Errorf("VM manager dependencies are required")
	}
	manager := &Manager{
		configuration:        configuration,
		store:                newMachineStore(configuration.MachinesDirectory),
		runtime:              dependencies.Runtime,
		network:              dependencies.Network,
		storage:              dependencies.Storage,
		snapshots:            dependencies.Snapshots,
		temporaryUserIDs:     make(map[uint32]bool),
		temporaryIdentifiers: make(map[string]bool),
	}
	if err := manager.store.validateAll(); err != nil {
		return nil, fmt.Errorf("validate VM records: %w", err)
	}
	return manager, nil
}

// Create reserves a VM or accepts a retry of the first request.
func (manager *Manager) Create(ctx context.Context, identifier string, specification Spec) (Info, error) {
	if !validIdentifier(identifier) {
		return Info{}, ErrConflict
	}
	unlock, err := manager.operationLocks.lock(ctx, identifier)
	if err != nil {
		return Info{}, err
	}
	defer unlock()

	fingerprint, err := createFingerprint(specification)
	if err != nil {
		return Info{}, err
	}
	existing, err := manager.store.readDesired(identifier)
	if err == nil {
		if existing.State == StateDestroyed || existing.CreateFingerprint != fingerprint {
			return Info{}, ErrConflict
		}
		existing.Specification = existing.Specification.RefreshImageSource(specification)
		if err := manager.store.writeDesired(existing); err != nil {
			return Info{}, err
		}
		return manager.information(identifier)
	}
	if !errors.Is(err, ErrNotFound) {
		return Info{}, err
	}

	manager.allocationMutex.Lock()
	if manager.temporaryIdentifiers[identifier] {
		manager.allocationMutex.Unlock()
		return Info{}, ErrConflict
	}
	defer manager.allocationMutex.Unlock()
	if inUse, err := manager.publicIPv4InUse(identifier, specification.Network.PublicIPv4); err != nil {
		return Info{}, err
	} else if inUse {
		return Info{}, ErrConflict
	}
	userID, err := manager.allocateUserID()
	if err != nil {
		return Info{}, err
	}
	desired := DesiredRecord{
		ID:                identifier,
		UserID:            userID,
		GroupID:           userID,
		CreateFingerprint: fingerprint,
		Generation:        1,
		State:             StateRunning,
		Specification:     cloneSpecification(specification),
	}
	observed := ObservedRecord{State: StateUnknown, UpdatedAt: time.Now().UTC()}
	if err := manager.store.writeDesired(desired); err != nil {
		return Info{}, err
	}
	if err := manager.store.writeObserved(identifier, observed); err != nil {
		return Info{}, errors.Join(err, manager.store.remove(identifier))
	}
	return informationFromRecords(desired, observed, DiskUsage{}), nil
}

// Information returns the persisted desired and observed VM state.
func (manager *Manager) Information(ctx context.Context, identifier string) (Info, error) {
	return manager.information(identifier)
}

// List returns all valid virtual machine records.
func (manager *Manager) List(ctx context.Context) ([]Info, error) {
	identifiers, err := manager.store.listIDs()
	if err != nil {
		return nil, err
	}
	information := make([]Info, 0, len(identifiers))
	for _, identifier := range identifiers {
		current, err := manager.information(identifier)
		if err != nil {
			return nil, err
		}
		information = append(information, current)
	}
	return information, nil
}

// ListIDs returns all reserved virtual machine identifiers.
func (manager *Manager) ListIDs(_ context.Context) ([]string, error) {
	return manager.store.listIDs()
}

func (manager *Manager) information(identifier string) (Info, error) {
	desired, err := manager.store.readDesired(identifier)
	if err != nil {
		return Info{}, err
	}
	observed, err := manager.store.readObserved(identifier)
	if err != nil {
		return Info{}, err
	}
	return informationFromRecords(desired, observed, observed.Disk), nil
}

func (manager *Manager) allocateUserID() (uint32, error) {
	identifiers, err := manager.store.listIDs()
	if err != nil {
		return 0, err
	}
	used := make(map[uint32]bool, len(identifiers))
	for _, identifier := range identifiers {
		record, err := manager.store.readDesired(identifier)
		if err != nil {
			return 0, err
		}
		used[record.UserID] = true
	}
	for userID := range manager.temporaryUserIDs {
		used[userID] = true
	}
	return manager.configuration.UserIDRange.Allocate(used)
}

func (manager *Manager) publicIPv4InUse(identifier, address string) (bool, error) {
	if address == "" {
		return false, nil
	}
	identifiers, err := manager.store.listIDs()
	if err != nil {
		return false, err
	}
	for _, existingIdentifier := range identifiers {
		if existingIdentifier == identifier {
			continue
		}
		record, err := manager.store.readDesired(existingIdentifier)
		if err != nil {
			return false, err
		}
		if record.State != StateDestroyed && record.Specification.Network.PublicIPv4 == address {
			return true, nil
		}
	}
	return false, nil
}

func validIdentifier(identifier string) bool {
	return identifier != "" && identifier != "." && filepath.Base(identifier) == identifier
}

func createFingerprint(specification Spec) (string, error) {
	normalized := cloneSpecification(specification)
	normalized.Image.RootfsURL = ""
	normalized.Image.KernelURL = ""
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode create fingerprint: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func cloneSpecification(specification Spec) Spec {
	specification.SSHKeys = slices.Clone(specification.SSHKeys)
	specification.Metadata = maps.Clone(specification.Metadata)
	if specification.Image.MemorySnapshotConfiguration != nil {
		configuration := *specification.Image.MemorySnapshotConfiguration
		specification.Image.MemorySnapshotConfiguration = &configuration
	}
	return specification
}

func informationFromRecords(desired DesiredRecord, observed ObservedRecord, usage DiskUsage) Info {
	errorDetail := ""
	if observed.Error != nil {
		errorDetail = observed.Error.Message
	}
	if usage.SizeMiB == 0 {
		usage.SizeMiB = desired.Specification.DiskMiB
	}
	return Info{
		ID:                            desired.ID,
		State:                         observed.State,
		DesiredState:                  desired.State,
		Error:                         errorDetail,
		VCPUs:                         desired.Specification.VCPUs,
		MemoryMiB:                     desired.Specification.MemoryMiB,
		DiskMiB:                       usage.SizeMiB,
		DiskUsedMiB:                   usage.UsedMiB,
		DiskThroughputMiBps:           desired.Specification.Disk.ThroughputMiBps,
		DiskIOPS:                      desired.Specification.Disk.IOPS,
		Image:                         desired.Specification.Image,
		SSHKeys:                       slices.Clone(desired.Specification.SSHKeys),
		Hostname:                      desired.Specification.Hostname,
		Metadata:                      maps.Clone(desired.Specification.Metadata),
		MAC:                           observed.NetworkInterface.MACAddress,
		PublicIPv4:                    desired.Specification.Network.PublicIPv4,
		WireGuardMeshIPv6:             desired.Specification.Network.WireGuardMeshIPv6,
		PrivateNetworkThroughputMiBps: desired.Specification.Network.PrivateNetworkThroughputMiBps,
		PublicNetworkThroughputMiBps:  desired.Specification.Network.PublicNetworkThroughputMiBps,
		Egress:                        desired.Specification.Network.Egress,
		DesiredGeneration:             desired.Generation,
		DesiredRestartGeneration:      desired.RestartGeneration,
		ObservedGeneration:            observed.Generation,
		ObservedRestartGeneration:     observed.RestartGeneration,
		Phase:                         observed.Phase,
		OperationID:                   observed.OperationID,
		OperationStartedAt:            observed.OperationStartedAt,
		UpdatedAt:                     observed.UpdatedAt,
	}
}

func newOperationID() string {
	identifier, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return identifier.String()
}

// RunTemporary runs an operation on a manager-owned temporary virtual machine.
func (manager *Manager) RunTemporary(
	ctx context.Context,
	identifier string,
	specification Spec,
	operation func(RuntimeMachine) error,
) (operationError error) {
	if !validIdentifier(identifier) {
		return ErrConflict
	}
	manager.allocationMutex.Lock()
	if manager.temporaryIdentifiers[identifier] {
		manager.allocationMutex.Unlock()
		return ErrConflict
	}
	if _, err := manager.store.readDesired(identifier); err == nil {
		manager.allocationMutex.Unlock()
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		manager.allocationMutex.Unlock()
		return err
	}
	userID, err := manager.allocateUserID()
	if err != nil {
		manager.allocationMutex.Unlock()
		return err
	}
	manager.temporaryUserIDs[userID] = true
	manager.temporaryIdentifiers[identifier] = true
	manager.allocationMutex.Unlock()
	defer func() {
		manager.allocationMutex.Lock()
		delete(manager.temporaryUserIDs, userID)
		delete(manager.temporaryIdentifiers, identifier)
		manager.allocationMutex.Unlock()
	}()
	desired := DesiredRecord{ID: identifier, UserID: userID, GroupID: userID, Specification: cloneSpecification(specification)}
	interfaceState, err := manager.network.Ensure(ctx, networkRequest(desired))
	if err != nil {
		_ = manager.network.Release(context.WithoutCancel(ctx), NetworkReleaseRequest{
			VirtualMachineID: identifier, UserID: userID,
			WireGuardMeshIPv6: specification.Network.WireGuardMeshIPv6,
		})
		return fmt.Errorf("ensure temporary VM network: %w", err)
	}
	machine := runtimeMachine(desired, interfaceState)
	defer func() {
		cleanupContext := context.WithoutCancel(ctx)
		operationError = errors.Join(
			operationError,
			manager.runtime.Remove(cleanupContext, machine),
			manager.network.Release(cleanupContext, NetworkReleaseRequest{
				VirtualMachineID: identifier, UserID: userID,
				WireGuardMeshIPv6: specification.Network.WireGuardMeshIPv6,
			}),
			manager.storage.Release(cleanupContext, identifier),
		)
	}()
	if err := manager.runtime.Start(ctx, machine); err != nil {
		return fmt.Errorf("start temporary VM: %w", err)
	}
	return operation(machine)
}
