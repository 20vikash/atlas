package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeRuntime struct {
	state        State
	starts       int
	startMode    StartMode
	stops        int
	stopMode     StopMode
	pauses       int
	resumes      int
	removes      int
	metadata     int
	diskRefresh  int
	inspectError error
	// startFromMemorySnapshotError makes snapshot restore fail.
	startFromMemorySnapshotError error
	// stopOutcome is returned by a warm stop.
	stopOutcome StopOutcome
	// sleepSnapshot and sleepSnapshotError control snapshot inspection.
	sleepSnapshot      SleepSnapshot
	sleepSnapshotError error
	// discards counts DiscardSleepSnapshot calls.
	discards int
}

func (runtime *fakeRuntime) Inspect(context.Context, RuntimeMachine) (RuntimeStatus, error) {
	return RuntimeStatus{State: runtime.state}, runtime.inspectError
}

func (runtime *fakeRuntime) Start(_ context.Context, _ RuntimeMachine, mode StartMode) error {
	runtime.startMode = mode
	if mode == StartFromSleepSnapshot && runtime.startFromMemorySnapshotError != nil {
		return runtime.startFromMemorySnapshotError
	}
	runtime.starts++
	runtime.state = StateRunning
	return nil
}

func (runtime *fakeRuntime) Stop(_ context.Context, _ RuntimeMachine, mode StopMode) (StopOutcome, error) {
	runtime.stops++
	runtime.stopMode = mode
	runtime.state = StateStopped
	return runtime.stopOutcome, nil
}

func (runtime *fakeRuntime) Pause(context.Context, RuntimeMachine) error {
	runtime.pauses++
	runtime.state = StatePaused
	return nil
}

func (runtime *fakeRuntime) Resume(context.Context, RuntimeMachine) error {
	runtime.resumes++
	runtime.state = StateRunning
	return nil
}

func (runtime *fakeRuntime) Remove(context.Context, RuntimeMachine) error {
	runtime.removes++
	runtime.state = StateDestroyed
	return nil
}

func (runtime *fakeRuntime) RefreshMetadata(context.Context, RuntimeMachine) error {
	runtime.metadata++
	return nil
}

func (runtime *fakeRuntime) RefreshDisk(context.Context, RuntimeMachine) error {
	runtime.diskRefresh++
	return nil
}

func (runtime *fakeRuntime) InspectSleepSnapshot(context.Context, RuntimeMachine) (SleepSnapshot, error) {
	return runtime.sleepSnapshot, runtime.sleepSnapshotError
}

func (runtime *fakeRuntime) DiscardSleepSnapshot(context.Context, RuntimeMachine) error {
	runtime.discards++
	runtime.state = StateStopped
	return nil
}

func (runtime *fakeRuntime) ConnectSSH(context.Context, RuntimeMachine) (SSHConnection, error) {
	return nil, nil
}

type fakeNetwork struct {
	ensures  int
	releases int
}

func (network *fakeNetwork) Ensure(context.Context, NetworkRequest) (NetworkInterface, error) {
	network.ensures++
	return NetworkInterface{MACAddress: "06:00:ac:10:00:02"}, nil
}

func (network *fakeNetwork) Release(context.Context, NetworkReleaseRequest) error {
	network.releases++
	return nil
}

type fakeStorage struct {
	resizes      int
	releases     int
	releaseError error
}

func (storage *fakeStorage) DiskUsage(context.Context, string) (DiskUsage, error) {
	return DiskUsage{}, nil
}

func (storage *fakeStorage) ResizeDisk(context.Context, string, int) error {
	storage.resizes++
	return nil
}

func (storage *fakeStorage) Release(context.Context, string) error {
	storage.releases++
	return storage.releaseError
}

type fakeSnapshots struct{}

func (fakeSnapshots) Stage(context.Context, SnapshotRequest) (StagedSnapshot, error) {
	return StagedSnapshot{}, nil
}

func newTestManager(t *testing.T) (*Manager, *fakeRuntime, *fakeNetwork, *fakeStorage) {
	t.Helper()
	runtime := &fakeRuntime{
		state:       StateStopped,
		stopOutcome: StopOutcome{MemorySnapshotGeneration: 1, MemorySnapshotCreatedAt: time.Unix(1000, 0).UTC()},
	}
	network := &fakeNetwork{}
	storage := &fakeStorage{}
	manager, err := NewManager(
		ManagerConfig{MachinesDirectory: t.TempDir(), UserIDRange: UserIDRange{Min: 1000, Max: 1010}},
		ManagerDependencies{
			Runtime:                runtime,
			Network:                network,
			Storage:                storage,
			Snapshots:              fakeSnapshots{},
			NetworkActivityMonitor: &fakeNetworkActivityMonitor{},
			NetworkWakeMonitor:     &fakeNetworkWakeMonitor{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager, runtime, network, storage
}

func testSpecification() Specification {
	return Specification{
		VirtualCPUCount: 2,
		MemoryMiB:       2048,
		DiskMiB:         4096,
		Image: Image{
			Name:         "image-1",
			Architecture: "amd64",
			RootfsURL:    "https://example.com/rootfs?token=first",
			RootfsSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			KernelURL:    "https://example.com/kernel?token=first",
			KernelSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		Network: NetworkConfiguration{Egress: EgressUplink, WireGuardMeshIPv6: "fdaa::2"},
	}
}

func TestCreateFingerprintAcceptsChangedSignedURLs(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	specification := testSpecification()
	if _, err := manager.Create(context.Background(), "machine-1", specification, SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	specification.Image.RootfsURL = "https://example.com/rootfs?token=second"
	specification.Image.KernelURL = "https://example.com/kernel?token=second"
	if _, err := manager.Create(context.Background(), "machine-1", specification, SleepPolicy{}); err != nil {
		t.Fatalf("create retry failed: %v", err)
	}
	record, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 1 {
		t.Fatalf("generation = %d, want 1", record.Generation)
	}
	if record.Specification.Image.RootfsURL != specification.Image.RootfsURL {
		t.Fatal("signed URL was not refreshed")
	}
}

func TestCreateFingerprintRejectsDifferentFirstRequest(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	specification := testSpecification()
	if _, err := manager.Create(context.Background(), "machine-1", specification, SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	specification.MemoryMiB++
	if _, err := manager.Create(context.Background(), "machine-1", specification, SleepPolicy{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("create error = %v, want conflict", err)
	}
}

func TestMutationGenerationChangesOnlyForNewValues(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPowerState(context.Background(), "machine-1", StateStopped); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPowerState(context.Background(), "machine-1", StateStopped); err != nil {
		t.Fatal(err)
	}
	record, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 2 {
		t.Fatalf("generation = %d, want 2", record.Generation)
	}
}

func TestSpecificationGenerationTracksShapeNotPower(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	base, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}

	// Power changes leave the specification generation unchanged.
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPowerState(context.Background(), "machine-1", StateRunning); err != nil {
		t.Fatal(err)
	}
	afterPower, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if afterPower.SpecificationGeneration != base.SpecificationGeneration {
		t.Errorf("power change moved specification generation %d to %d", base.SpecificationGeneration, afterPower.SpecificationGeneration)
	}
	if afterPower.Generation == base.Generation {
		t.Error("power change did not raise the desired generation")
	}

	// A shape change raises the specification generation.
	configuration := testSpecification().Network
	configuration.PublicNetworkThroughputMiBps = 500
	if err := manager.SetNetwork(context.Background(), "machine-1", configuration); err != nil {
		t.Fatal(err)
	}
	afterShape, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if afterShape.SpecificationGeneration <= afterPower.SpecificationGeneration {
		t.Error("a network change did not raise the specification generation")
	}
}

func TestStopWarmStoresTheWarmStopIntent(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	// A repeat with the same intent must not change the record.
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	record, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateStopped || !record.WarmStop {
		t.Fatalf("desired record = %+v", record)
	}
	if record.Generation != 2 {
		t.Fatalf("generation = %d, want 2", record.Generation)
	}

	// A plain stop clears the warm intent and raises the generation.
	if err := manager.SetPowerState(context.Background(), "machine-1", StateStopped); err != nil {
		t.Fatal(err)
	}
	record, err = manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.WarmStop {
		t.Error("a plain stop did not clear the warm intent")
	}
	if record.Generation != 3 {
		t.Fatalf("generation = %d, want 3", record.Generation)
	}
}

func TestWarmStopReconcilesToSleepingThenResumes(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	// A warm stop terminates the VM and reaches the sleeping observed state.
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.stopMode != StopWithSleepSnapshot {
		t.Fatalf("stop mode = %d, want StopWithSleepSnapshot", runtime.stopMode)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping || observed.Sleep == nil || observed.Sleep.MemorySnapshotGeneration != 1 {
		t.Fatalf("observed after warm stop = %+v", observed)
	}

	// Starting again resumes from the snapshot and clears the sleep progress.
	if err := manager.SetPowerState(context.Background(), "machine-1", StateRunning); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.startMode != StartFromSleepSnapshot {
		t.Fatalf("start mode = %d, want StartFromSleepSnapshot", runtime.startMode)
	}
	observed, err = manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateRunning || observed.Sleep != nil {
		t.Fatalf("observed after resume = %+v", observed)
	}
}

func TestStopOutsideTheAPIColdBoots(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	// The guest stops itself, so no warm stop set the resume marker.
	runtime.state = StateStopped
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.startMode != StartNormal {
		t.Fatalf("start mode = %d, want StartNormal", runtime.startMode)
	}
}

func TestSnapshotResumeFailureKeepsSleeping(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	// A restore failure keeps the VM sleeping and never cold boots.
	runtime.startFromMemorySnapshotError = errors.New("snapshot incompatible")
	if err := manager.SetPowerState(context.Background(), "machine-1", StateRunning); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err == nil {
		t.Fatal("expected the resume failure to surface")
	}
	if runtime.startMode == StartNormal {
		t.Fatal("a resume failure must not cold boot")
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping || observed.Sleep == nil {
		t.Fatalf("observed after resume failure = %+v", observed)
	}
}

func TestWarmStoppedVMStaysSleeping(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopWarm(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}

	// A later pass holds the VM asleep. It does not snapshot again or resume.
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.stops != 1 {
		t.Fatalf("stops = %d, want one warm stop", runtime.stops)
	}
	if runtime.startMode == StartFromSleepSnapshot {
		t.Fatal("a held sleeping VM must not resume")
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateSleeping {
		t.Fatalf("observed = %+v, want sleeping", observed)
	}
}

func TestSetComputeRequestsRunningState(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPowerState(context.Background(), "machine-1", StateStopped); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetCompute(context.Background(), "machine-1", Compute{VirtualCPUCount: 4, MemoryMiB: 4096}); err != nil {
		t.Fatal(err)
	}
	record, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateRunning || record.Generation != 3 {
		t.Fatalf("desired record = %+v", record)
	}
}

// A sleep policy change is accepted while the VM runs, and it leaves the power
// state and the specification generation alone.
func TestSetComputeStoresTheSleepPolicyWithoutAShapeChange(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	before, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}

	compute := Compute{
		VirtualCPUCount: before.Specification.VirtualCPUCount,
		MemoryMiB:       before.Specification.MemoryMiB,
		Sleep:           SleepPolicy{IsSleepy: true, IdleTimeoutSeconds: 1800},
	}
	if err := manager.SetCompute(context.Background(), "machine-1", compute); err != nil {
		t.Fatal(err)
	}
	// A repeat with the same values must not change the record.
	if err := manager.SetCompute(context.Background(), "machine-1", compute); err != nil {
		t.Fatal(err)
	}

	record, err := manager.store.readDesired("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Sleep != compute.Sleep {
		t.Errorf("sleep policy = %+v, want %+v", record.Sleep, compute.Sleep)
	}
	if record.State != StateRunning {
		t.Errorf("power state changed to %s", record.State)
	}
	if record.SpecificationGeneration != before.SpecificationGeneration {
		t.Errorf("specification generation = %d, want %d", record.SpecificationGeneration, before.SpecificationGeneration)
	}
	if record.Generation != 2 {
		t.Fatalf("generation = %d, want 2", record.Generation)
	}
}

func TestSetPowerStateRejectsSleeping(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPowerState(context.Background(), "machine-1", StateSleeping); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestSetComputeRejectsAMissingVirtualMachine(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if err := manager.SetCompute(context.Background(), "missing", Compute{VirtualCPUCount: 1, MemoryMiB: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestSetComputeConflictsWithADestroyedVirtualMachine(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	sleepy := Compute{VirtualCPUCount: 1, MemoryMiB: 1, Sleep: SleepPolicy{IsSleepy: true, IdleTimeoutSeconds: 60}}
	if err := manager.SetCompute(context.Background(), "machine-1", sleepy); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestNewManagerRejectsUnknownAndTrailingRecordData(t *testing.T) {
	for _, data := range []string{
		`{"schema_version":1,"id":"machine-1","unknown":true}`,
		`{"schema_version":1,"id":"machine-1"} {}`,
		`{"id":"machine-1"}`,
	} {
		directory := t.TempDir()
		machineDirectory := filepath.Join(directory, "machine-1")
		if err := os.MkdirAll(machineDirectory, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(machineDirectory, "config.json"), []byte(data), 0o640); err != nil {
			t.Fatal(err)
		}
		_, err := NewManager(
			ManagerConfig{MachinesDirectory: directory},
			ManagerDependencies{Runtime: &fakeRuntime{}, Network: &fakeNetwork{}, Storage: &fakeStorage{}, Snapshots: fakeSnapshots{}, NetworkActivityMonitor: &fakeNetworkActivityMonitor{}, NetworkWakeMonitor: &fakeNetworkWakeMonitor{}},
		)
		if err == nil {
			t.Fatalf("NewManager accepted record %s", data)
		}
	}
}

// A create or a destroy leaves a directory with one record for a short time.
// Capacity reads must not fail for the whole host in that window.
func TestListSkipsIncompleteRecords(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	for _, identifier := range []string{"machine-1", "machine-2"} {
		if _, err := manager.Create(context.Background(), identifier, testSpecification(), SleepPolicy{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(manager.store.observedPath("machine-2")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(manager.store.directory, "machine-3"), 0o700); err != nil {
		t.Fatal(err)
	}

	information, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if len(information) != 1 || information[0].ID != "machine-1" {
		t.Fatalf("information = %+v", information)
	}
}

// A record that is present but corrupt is a host fault, not a VM in flux.
func TestListReportsCorruptRecords(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.store.observedPath("machine-1"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.List(context.Background()); err == nil {
		t.Fatal("List accepted a corrupt record")
	}
}

func TestReconcileUsesRuntimeAndResourceDependencies(t *testing.T) {
	manager, runtime, network, storage := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 1 || runtime.metadata != 1 || runtime.diskRefresh != 1 {
		t.Fatalf("runtime calls = start %d, metadata %d, disk %d", runtime.starts, runtime.metadata, runtime.diskRefresh)
	}
	if network.ensures != 1 || storage.resizes != 1 {
		t.Fatalf("resource calls = network %d, storage %d", network.ensures, storage.resizes)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.Generation != 1 || observed.State != StateRunning {
		t.Fatalf("observed record = %+v", observed)
	}
}

func TestCleanupProgressSurvivesRetry(t *testing.T) {
	manager, runtime, network, storage := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	storage.releaseError = errors.New("storage unavailable")
	if err := manager.Reconcile(context.Background(), "machine-1"); err == nil {
		t.Fatal("cleanup succeeded while storage failed")
	}
	storage.releaseError = nil
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.removes != 1 || network.releases != 1 || storage.releases != 2 {
		t.Fatalf("cleanup calls = runtime %d, network %d, storage %d", runtime.removes, network.releases, storage.releases)
	}
	if _, err := manager.Information(context.Background(), "machine-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("information error = %v, want not found", err)
	}
}

func TestRestartIntentSurvivesManagerRecreation(t *testing.T) {
	manager, runtime, network, storage := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.RequestRestart(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	recreated, err := NewManager(manager.configuration, ManagerDependencies{
		Runtime: runtime, Network: network, Storage: storage, Snapshots: fakeSnapshots{},
		NetworkActivityMonitor: &fakeNetworkActivityMonitor{}, NetworkWakeMonitor: &fakeNetworkWakeMonitor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recreated.Reconcile(context.Background(), "machine-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.stops != 1 || runtime.starts != 2 {
		t.Fatalf("restart calls = stops %d, starts %d", runtime.stops, runtime.starts)
	}
	// A guest restart is a normal shutdown followed by a normal start.
	if runtime.stopMode != StopShutdown || runtime.startMode != StartNormal {
		t.Fatalf("restart used stop mode %d and start mode %d, want shutdown then normal", runtime.stopMode, runtime.startMode)
	}
	observed, err := recreated.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.RestartGeneration != 1 {
		t.Fatalf("restart generation = %d, want 1", observed.RestartGeneration)
	}
}

func TestInspectFailureStoresSafeAndLocalErrors(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	runtime.inspectError = errors.New("socket /private/path failed")
	if err := manager.Reconcile(context.Background(), "machine-1"); err == nil {
		t.Fatal("reconcile succeeded while inspection failed")
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateUnknown || observed.Error == nil {
		t.Fatalf("observed record = %+v", observed)
	}
	if observed.Error.Message != "inspect operation failed" {
		t.Fatalf("safe message = %q", observed.Error.Message)
	}
	if observed.Error.LocalDetail != "socket /private/path failed" {
		t.Fatalf("local detail = %q", observed.Error.LocalDetail)
	}
	information, err := manager.Information(context.Background(), "machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if information.Error == nil || information.Error.Message != observed.Error.Message {
		t.Fatalf("public error = %+v", information.Error)
	}
}

func TestCreateSnapshotRejectsUnknownRuntimeState(t *testing.T) {
	manager, runtime, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	runtime.state = StateUnknown
	if _, err := manager.CreateSnapshot(context.Background(), "machine-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("snapshot error = %v, want conflict", err)
	}
	observed, err := manager.store.readObserved("machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != StateUnknown || observed.Phase != "" || observed.OperationID != "" {
		t.Fatalf("observed record = %+v", observed)
	}
}

// The runtime makes a machine directory for a temporary VM. A directory that
// stays behind holds no records, so it stops every later start and list.
func TestRunTemporaryRemovesMachineDirectory(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	identifier := "warm-529e5118abe6"
	run := func(RuntimeMachine) error {
		return os.MkdirAll(filepath.Join(manager.store.directory, identifier, "firecracker"), 0o700)
	}
	if err := manager.RunTemporary(context.Background(), identifier, testSpecification(), run); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(manager.store.directory, identifier)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("machine directory stat = %v, want not exist", err)
	}
}

// A host that stops during a temporary build keeps the directory. metald must
// still start, because a directory without a desired record is not a VM.
func TestNewManagerStartsWithLeftoverTemporaryDirectory(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	if _, err := manager.Create(context.Background(), "machine-1", testSpecification(), SleepPolicy{}); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(manager.store.directory, "warm-529e5118abe6", "firecracker")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := manager.store.validateAll(); err != nil {
		t.Fatalf("validateAll = %v", err)
	}
	identifiers, err := manager.store.listIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(identifiers) != 1 || identifiers[0] != "machine-1" {
		t.Fatalf("identifiers = %v", identifiers)
	}
}

func TestRunTemporaryRejectsInvalidIdentifier(t *testing.T) {
	manager, _, _, _ := newTestManager(t)
	err := manager.RunTemporary(context.Background(), "../machine-1", testSpecification(), func(RuntimeMachine) error {
		return nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("temporary machine error = %v, want conflict", err)
	}
}
