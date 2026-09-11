package vmmigration

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

// fakeMachines implements Machines with in-memory records and call counters.
type fakeMachines struct {
	dir      string
	desired  map[string]vm.DesiredRecord
	observed map[string]vm.ObservedRecord

	nextUserID   uint32
	limitDiskCap int

	normalizeCalls      int
	removeNetworkCalls  int
	ensureNetworkCalls  int
	applyStateCalls     int
	restoreCalls        int
	removeRuntimeCalls  int
	releaseStorageCalls int
	refreshDiskCalls    int
	limitDiskCalls      int
	limitDiskValue      int

	applyStateError error
}

func newFakeMachines(t *testing.T) *fakeMachines {
	t.Helper()
	return &fakeMachines{
		dir:        t.TempDir(),
		desired:    make(map[string]vm.DesiredRecord),
		observed:   make(map[string]vm.ObservedRecord),
		nextUserID: 100001,
	}
}

// create seeds a running source VM with a valid create fingerprint.
func (f *fakeMachines) create(id string, specification vm.Specification) {
	f.desired[id] = vm.DesiredRecord{
		ID:                id,
		UserID:            1000,
		GroupID:           1000,
		CreateFingerprint: strings.Repeat("a", 64),
		Generation:        1,
		State:             vm.StateRunning,
		Specification:     specification,
	}
	f.observed[id] = vm.ObservedRecord{State: vm.StateUnknown, UpdatedAt: time.Now().UTC()}
}

func (f *fakeMachines) ReadDesired(virtualMachineID string) (vm.DesiredRecord, error) {
	record, ok := f.desired[virtualMachineID]
	if !ok {
		return vm.DesiredRecord{}, vm.ErrNotFound
	}
	return record, nil
}

func (f *fakeMachines) ReadObserved(virtualMachineID string) (vm.ObservedRecord, error) {
	record, ok := f.observed[virtualMachineID]
	if !ok {
		return vm.ObservedRecord{}, vm.ErrNotFound
	}
	return record, nil
}

func (f *fakeMachines) WriteDesired(record vm.DesiredRecord) error {
	f.desired[record.ID] = record
	return nil
}

func (f *fakeMachines) WriteObserved(virtualMachineID string, record vm.ObservedRecord) error {
	f.observed[virtualMachineID] = record
	return nil
}

func (f *fakeMachines) RemoveRecords(virtualMachineID string) error {
	delete(f.desired, virtualMachineID)
	delete(f.observed, virtualMachineID)
	return nil
}

func (f *fakeMachines) DesiredPath(virtualMachineID string) string {
	return filepath.Join(f.dir, virtualMachineID, "config.json")
}

func (f *fakeMachines) ObservedPath(virtualMachineID string) string {
	return filepath.Join(f.dir, virtualMachineID, "status.json")
}

func (f *fakeMachines) AllocateUserID() (uint32, error) {
	userID := f.nextUserID
	f.nextUserID++
	return userID, nil
}

func (f *fakeMachines) LockAllocation() func() { return func() {} }

func (f *fakeMachines) LockOperation(context.Context, string) (func(), error) {
	return func() {}, nil
}

func (f *fakeMachines) ReleaseStorage(context.Context, string) error {
	f.releaseStorageCalls++
	return nil
}

func (f *fakeMachines) MachinesDirectory() string { return f.dir }

func (f *fakeMachines) NormalizeSourceToStopped(context.Context, string) error {
	f.normalizeCalls++
	return nil
}

func (f *fakeMachines) RemoveMigrationNetwork(context.Context, string) error {
	f.removeNetworkCalls++
	return nil
}

func (f *fakeMachines) EnsureMigrationNetwork(context.Context, string) error {
	f.ensureNetworkCalls++
	return nil
}

func (f *fakeMachines) ApplyMigratedTargetState(context.Context, string) error {
	f.applyStateCalls++
	return f.applyStateError
}

func (f *fakeMachines) RestoreRuntimeState(context.Context, string, vm.State) error {
	f.restoreCalls++
	return nil
}

func (f *fakeMachines) RemoveMigratedRuntime(context.Context, string) error {
	f.removeRuntimeCalls++
	return nil
}

func (f *fakeMachines) RefreshSourceDisk(context.Context, string) error {
	f.refreshDiskCalls++
	return nil
}

func (f *fakeMachines) LimitSourceDisk(_ context.Context, _ string, throughputMiBps int) (int, error) {
	f.limitDiskCalls++
	if f.limitDiskCap > 0 && throughputMiBps > f.limitDiskCap {
		throughputMiBps = f.limitDiskCap
	}
	f.limitDiskValue = throughputMiBps
	return throughputMiBps, nil
}

func testSpecification() vm.Specification {
	return vm.Specification{
		VirtualCPUCount: 2,
		MemoryMiB:       2048,
		DiskMiB:         4096,
	}
}

type fakeTransfer struct {
	created       []string
	removed       []string
	sent          []string
	guid          string
	sizeBytes     int64
	sentBytes     int64
	datasetExists bool
	resumeToken   string
	received      int
	aborts        int
	sendErr       error
	receiveErr    error
	abortErr      error
}

func (f *fakeTransfer) CreateSnapshot(_ context.Context, _, name string) error {
	f.created = append(f.created, name)
	return nil
}

func (f *fakeTransfer) RemoveSnapshot(_ context.Context, _, name string) error {
	f.removed = append(f.removed, name)
	return nil
}

func (f *fakeTransfer) SnapshotGUID(_ context.Context, _, _ string) (string, error) {
	return f.guid, nil
}

func (f *fakeTransfer) EstimateStreamBytes(_ context.Context, _, _, _ string) (int64, error) {
	return f.sizeBytes, nil
}

func (f *fakeTransfer) SendSnapshot(_ context.Context, _, name, base, token string, w io.Writer) (int64, error) {
	f.sent = append(f.sent, name+"|"+base+"|"+token)
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	if f.sentBytes > 0 {
		_, _ = w.Write(make([]byte, f.sentBytes))
	}
	return f.sentBytes, nil
}

func (f *fakeTransfer) TargetDatasetExists(_ context.Context, _ string) (bool, error) {
	return f.datasetExists, nil
}

func (f *fakeTransfer) ReceiveResumeToken(_ context.Context, _ string) (string, error) {
	return f.resumeToken, nil
}

func (f *fakeTransfer) ReceiveSnapshot(_ context.Context, _ string, r io.Reader) error {
	f.received++
	_, _ = io.Copy(io.Discard, r)
	return f.receiveErr
}

func (f *fakeTransfer) AbortReceive(_ context.Context, _ string) error {
	f.aborts++
	return f.abortErr
}

type fakeSourceClient struct {
	prepareCalls  int
	removeCalls   int
	removeError   error
	prepareConfig PortableConfig
	prepareState  vm.State
	prepareError  error
	nextSnapshot  SourceSnapshot
	nextQueue     []SourceSnapshot
	nextError     error
	nextCalls     int
	nextSequences []int
	streamBytes   int64
	streamError   error
	streamHang    bool
	streamMiBps   []int
	stopSnapshot  SourceSnapshot
	stopError     error
	stopCalls     int
	startCalls    int
	startError    error
	finishCalls   int
	finishError   error
}

func (c *fakeSourceClient) PrepareSource(context.Context, string, string, string) (PortableConfig, vm.State, error) {
	c.prepareCalls++
	return c.prepareConfig, c.prepareState, c.prepareError
}

func (c *fakeSourceClient) NextSnapshot(_ context.Context, _, _, _ string, receivedSequence int) (SourceSnapshot, error) {
	c.nextSequences = append(c.nextSequences, receivedSequence)
	index := c.nextCalls
	c.nextCalls++
	if c.nextError != nil {
		return SourceSnapshot{}, c.nextError
	}
	if index < len(c.nextQueue) {
		return c.nextQueue[index], nil
	}
	if len(c.nextQueue) > 0 {
		return SourceSnapshot{}, errors.New("no more snapshots")
	}
	return c.nextSnapshot, nil
}

func (c *fakeSourceClient) StreamSnapshot(ctx context.Context, _, _, _ string, _ int, _ string, throughputMiBps int, w io.Writer) (int64, error) {
	c.streamMiBps = append(c.streamMiBps, throughputMiBps)
	if c.streamHang {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if c.streamError != nil {
		return 0, c.streamError
	}
	if c.streamBytes > 0 {
		_, _ = w.Write(make([]byte, c.streamBytes))
	}
	return c.streamBytes, nil
}

func (c *fakeSourceClient) StopSource(context.Context, string, string, string) (SourceSnapshot, error) {
	c.stopCalls++
	return c.stopSnapshot, c.stopError
}

func (c *fakeSourceClient) StartSource(context.Context, string, string, string) error {
	c.startCalls++
	return c.startError
}

func (c *fakeSourceClient) FinishSource(context.Context, string, string, string) error {
	c.finishCalls++
	return c.finishError
}

func (c *fakeSourceClient) RemoveSource(context.Context, string, string, string) error {
	c.removeCalls++
	return c.removeError
}

func ampleCapacity(context.Context) (AvailableCapacity, error) {
	return AvailableCapacity{CPUCount: 64, MemoryMiB: 262144, StorageMiB: 4194304}, nil
}

func newMigrationManager(t *testing.T) (*VMMigration, *fakeMachines, *fakeSourceClient) {
	t.Helper()
	machines := newFakeMachines(t)
	source := &fakeSourceClient{}
	migrationManager, err := NewVMMigration(machines, source, &fakeTransfer{}, ampleCapacity, MigrationSettings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return migrationManager, machines, source
}

func TestNewVMMigrationCleansACorruptRecord(t *testing.T) {
	machines := newFakeMachines(t)
	store := newMigrationStore(machines.MachinesDirectory())
	migrationDirectory := store.migrationDirectory("vm-1")
	if err := os.MkdirAll(migrationDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	// A record left by a crashed or older-schema migration must not block startup.
	if err := os.WriteFile(filepath.Join(migrationDirectory, sourceFileName), []byte(`{"schema_version":1,"id":"mig-1"}`), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := NewVMMigration(machines, &fakeSourceClient{}, &fakeTransfer{}, ampleCapacity, MigrationSettings{}, nil); err != nil {
		t.Fatalf("manager did not start over a corrupt record: %v", err)
	}
	if store.has(store.sourcePath("vm-1")) {
		t.Fatal("the corrupt source record was not cleaned at startup")
	}
}

func TestCreateTargetReservesAndAcceptsARetry(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()

	record, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationRunning || record.Phase != PhasePreparing {
		t.Fatalf("record = %+v", record)
	}
	if !migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("VM ID was not reserved")
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatalf("idempotent retry = %v", err)
	}
}

func TestCreateTargetRejectsChangedValues(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.9:9000"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("changed source = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.CreateTarget(ctx, "mig-2", "vm-1", "http://10.0.0.3:9000"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("changed migration ID = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-2", "http://10.0.0.3:9000"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("reused migration ID for another VM = %v, want ErrConflict", err)
	}
}

func TestCreateTargetRejectsALiveVirtualMachineID(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	machines.create("vm-1", testSpecification())

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("reserve a live VM ID = %v, want ErrConflict", err)
	}
}

func TestTargetStatusResolvesTheMigrationID(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	record, err := migrationManager.TargetStatus(ctx, "mig-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.VirtualMachineID != "vm-1" {
		t.Fatalf("status = %+v", record)
	}
	if _, err := migrationManager.TargetStatus(ctx, "mig-missing"); !errors.Is(err, vm.ErrNotFound) {
		t.Fatalf("missing status = %v, want ErrNotFound", err)
	}
}

func TestAbortTargetUnlocksTheSourceAndClearsTheReservation(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AbortTarget(ctx, "mig-1"); err != nil {
		t.Fatal(err)
	}
	// Let the worker roll back, then wait for it.
	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if source.removeCalls != 1 {
		t.Fatalf("RemoveSource calls = %d, want 1", source.removeCalls)
	}
	if migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("reservation still present after abort")
	}
}

func TestAbortTargetKeepsRecordsWhenRollbackFails(t *testing.T) {
	migrationManager, _, source := newMigrationManager(t)
	source.removeError = errors.New("source unreachable")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AbortTarget(ctx, "mig-1"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.AdvanceTarget(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if err := migrationManager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !migrationManager.IsTargetReserved("vm-1") {
		t.Fatal("reservation was cleared despite a rollback failure")
	}
}

func TestTargetReservationsCountOnlyMigrationsWithConfig(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000"); err != nil {
		t.Fatal(err)
	}

	reservations, err := migrationManager.TargetReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 0 {
		t.Fatalf("preparing migration reserved capacity: %+v", reservations)
	}

	store := newMigrationStore(machines.MachinesDirectory())
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = PhaseCopying
	record.Config = &PortableConfig{Specification: vm.Specification{VirtualCPUCount: 3, MemoryMiB: 3072, DiskMiB: 8192}}
	if err := store.writeTarget(record); err != nil {
		t.Fatal(err)
	}

	reservations, err = migrationManager.TargetReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 1 || reservations[0].VirtualCPUCount != 3 || reservations[0].MemoryMiB != 3072 {
		t.Fatalf("reservations = %+v", reservations)
	}
}
