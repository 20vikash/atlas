package vm

import (
	"context"
	"errors"
	"io"
	"testing"
)

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
	sendErr       error
	receiveErr    error
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

type fakeSourceClient struct {
	prepareCalls  int
	removeCalls   int
	removeError   error
	prepareConfig PortableConfig
	prepareState  State
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

func (c *fakeSourceClient) PrepareSource(context.Context, string, string, string, string) (PortableConfig, State, error) {
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

func newMigrationManager(t *testing.T) (*MigrationManager, *Manager, *fakeSourceClient) {
	t.Helper()
	machines, _, _, _ := newTestManager(t)
	source := &fakeSourceClient{}
	migrationManager, err := NewMigrationManager(machines, source, &fakeTransfer{}, ampleCapacity, MigrationSettings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return migrationManager, machines, source
}

func TestCreateTargetReservesAndRefreshesTheToken(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()

	record, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != MigrationRunning || record.Phase != PhasePreparing {
		t.Fatalf("record = %+v", record)
	}
	if !machines.isTargetReserved("vm-1") {
		t.Fatal("VM ID was not reserved")
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-2"); err != nil {
		t.Fatalf("idempotent retry = %v", err)
	}
	token, err := newMigrationStore(machines.configuration.MachinesDirectory).readToken("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok-2" {
		t.Fatalf("token = %q, want the refreshed value", token)
	}
}

func TestCreateTargetRejectsChangedValues(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.9:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.CreateTarget(ctx, "mig-2", "vm-1", "http://10.0.0.3:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed migration ID = %v, want ErrConflict", err)
	}
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-2", "http://10.0.0.3:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused migration ID for another VM = %v, want ErrConflict", err)
	}
}

func TestCreateTargetRejectsALiveVirtualMachineID(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := machines.Create(ctx, "vm-1", testSpecification()); err != nil {
		t.Fatal(err)
	}

	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reserve a live VM ID = %v, want ErrConflict", err)
	}
}

func TestCreateRejectsATargetReservedID(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-9", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := machines.Create(ctx, "vm-9", testSpecification()); !errors.Is(err, ErrConflict) {
		t.Fatalf("create a target-reserved ID = %v, want ErrConflict", err)
	}
}

func TestTargetStatusResolvesTheMigrationID(t *testing.T) {
	migrationManager, _, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	record, err := migrationManager.TargetStatus(ctx, "mig-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.VirtualMachineID != "vm-1" {
		t.Fatalf("status = %+v", record)
	}
	if _, err := migrationManager.TargetStatus(ctx, "mig-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing status = %v, want ErrNotFound", err)
	}
}

func TestAbortTargetUnlocksTheSourceAndClearsTheReservation(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AbortTarget(ctx, "mig-1"); err != nil {
		t.Fatal(err)
	}
	if source.removeCalls != 1 {
		t.Fatalf("RemoveSource calls = %d, want 1", source.removeCalls)
	}
	if machines.isTargetReserved("vm-1") {
		t.Fatal("reservation still present after abort")
	}
}

func TestAbortTargetKeepsRecordsWhenCleanupFails(t *testing.T) {
	migrationManager, machines, source := newMigrationManager(t)
	source.removeError = errors.New("source unreachable")
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	if err := migrationManager.AbortTarget(ctx, "mig-1"); err == nil {
		t.Fatal("want a cleanup error")
	}
	if !machines.isTargetReserved("vm-1") {
		t.Fatal("reservation was cleared despite a cleanup failure")
	}
}

func TestTargetReservationsCountOnlyMigrationsWithConfig(t *testing.T) {
	migrationManager, machines, _ := newMigrationManager(t)
	ctx := context.Background()
	if _, err := migrationManager.CreateTarget(ctx, "mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"); err != nil {
		t.Fatal(err)
	}

	reservations, err := migrationManager.TargetReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 0 {
		t.Fatalf("preparing migration reserved capacity: %+v", reservations)
	}

	store := newMigrationStore(machines.configuration.MachinesDirectory)
	record, err := store.readTarget("vm-1")
	if err != nil {
		t.Fatal(err)
	}
	record.Phase = PhaseCopying
	record.Config = &PortableConfig{Specification: Specification{VirtualCPUCount: 3, MemoryMiB: 3072, DiskMiB: 8192}}
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
