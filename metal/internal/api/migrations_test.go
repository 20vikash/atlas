package api

import (
	"context"
	"encoding/json"
	vmmigration "github.com/frappe/atlas/metal/internal/vm_migration"
	"net/http"
	"testing"

	"github.com/frappe/atlas/metal/internal/host"
	"github.com/frappe/atlas/metal/internal/vm"
)

type stubMigrationManager struct {
	record           vmmigration.TargetMigrationRecord
	createErr        error
	statusErr        error
	abortErr         error
	handshake        vmmigration.SourceHandshake
	lockErr          error
	unlockErr        error
	snapshot         vmmigration.SourceSnapshot
	snapshotErr      error
	createArgs       []string
	lockArgs         []string
	unlockArgs       []string
	snapshotArgs     []string
	abortedID        string
	finishedID       string
	finishErr        error
	receivedSequence int
	stopArgs         []string
	stopErr          error
	startArgs        []string
	startErr         error
	destroyArgs      []string
	destroyErr       error
}

func (m *stubMigrationManager) CreateTarget(_ context.Context, migrationID, virtualMachineID, source string) (vmmigration.TargetMigrationRecord, error) {
	m.createArgs = []string{migrationID, virtualMachineID, source}
	return m.record, m.createErr
}

func (m *stubMigrationManager) TargetStatus(context.Context, string) (vmmigration.TargetMigrationRecord, error) {
	return m.record, m.statusErr
}

func (m *stubMigrationManager) RequestFinish(_ context.Context, migrationID string) error {
	m.finishedID = migrationID
	return m.finishErr
}

func (m *stubMigrationManager) AbortTarget(_ context.Context, migrationID string) error {
	m.abortedID = migrationID
	return m.abortErr
}

func (m *stubMigrationManager) LockSource(_ context.Context, migrationID, virtualMachineID string) (vmmigration.SourceHandshake, error) {
	m.lockArgs = []string{migrationID, virtualMachineID}
	return m.handshake, m.lockErr
}

func (m *stubMigrationManager) NextSourceSnapshot(_ context.Context, migrationID, virtualMachineID string, receivedSequence int) (vmmigration.SourceSnapshot, error) {
	m.receivedSequence = receivedSequence
	m.snapshotArgs = []string{migrationID, virtualMachineID}
	return m.snapshot, m.snapshotErr
}

func (m *stubMigrationManager) StopSource(_ context.Context, migrationID, virtualMachineID string) (vmmigration.SourceSnapshot, error) {
	m.stopArgs = []string{migrationID, virtualMachineID}
	return m.snapshot, m.stopErr
}

func (m *stubMigrationManager) StartSourceRollback(_ context.Context, migrationID, virtualMachineID string) error {
	m.startArgs = []string{migrationID, virtualMachineID}
	return m.startErr
}

func (m *stubMigrationManager) DestroySource(_ context.Context, migrationID, virtualMachineID string) error {
	m.destroyArgs = []string{migrationID, virtualMachineID}
	return m.destroyErr
}

func (m *stubMigrationManager) UnlockSource(_ context.Context, migrationID, virtualMachineID string) error {
	m.unlockArgs = []string{migrationID, virtualMachineID}
	return m.unlockErr
}

func newMigrationTestServer(t *testing.T, migrations MigrationManager) http.Handler {
	t.Helper()
	services := newFakeRuntimeServices()
	manager := &fakeVirtualMachineManager{virtualMachines: map[string]*fakeVM{}, services: services}
	hostService, err := host.NewService(host.Dependencies{
		Mesh: services, WireGuard: &fakeWireGuardManager{}, Images: services,
		VirtualMachines: manager, Storage: fakeCapacityProvider{}, Wake: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{AuthTokenHash: testTokenHash}, Dependencies{
		VirtualMachineManager: manager,
		MigrationManager:      migrations,
		SnapshotStore:         services,
		WakeReconciler:        func() {},
		HostService:           hostService,
		SerialBroker:          stubSerialBroker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestCreateMigrationDrivesTheTarget(t *testing.T) {
	stub := &stubMigrationManager{record: vmmigration.TargetMigrationRecord{ID: "mig-1", VirtualMachineID: "vm-1", Status: vmmigration.MigrationRunning, Phase: vmmigration.PhasePreparing}}
	server := newMigrationTestServer(t, stub)

	body := `{"virtual_machine_id":"vm-1","source":"http://10.0.0.3:9000"}`
	recorder := do(t, server, http.MethodPut, "/v1/migrations/mig-1", body, http.StatusAccepted)

	if want := []string{"mig-1", "vm-1", "http://10.0.0.3:9000"}; !equalStrings(stub.createArgs, want) {
		t.Fatalf("create args = %v", stub.createArgs)
	}
	var response migrationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "mig-1" || response.Status != "running" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCreateMigrationRejectsAMissingField(t *testing.T) {
	server := newMigrationTestServer(t, &stubMigrationManager{})
	do(t, server, http.MethodPut, "/v1/migrations/mig-1", `{"virtual_machine_id":"vm-1"}`, http.StatusBadRequest)
}

func TestGetAndAbortMigration(t *testing.T) {
	stub := &stubMigrationManager{record: vmmigration.TargetMigrationRecord{ID: "mig-1", VirtualMachineID: "vm-1", Status: vmmigration.MigrationReady, Phase: vmmigration.PhaseCopying}}
	server := newMigrationTestServer(t, stub)

	recorder := do(t, server, http.MethodGet, "/v1/migrations/mig-1", "", http.StatusOK)
	var response migrationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ready" {
		t.Fatalf("status = %s", response.Status)
	}

	do(t, server, http.MethodPost, "/v1/migrations/mig-1/abort", "", http.StatusAccepted)
	if stub.abortedID != "mig-1" {
		t.Fatalf("aborted = %q", stub.abortedID)
	}
}

func TestGetMigrationReportsTransferProgress(t *testing.T) {
	stub := &stubMigrationManager{record: vmmigration.TargetMigrationRecord{
		ID: "mig-1", VirtualMachineID: "vm-1", Status: vmmigration.MigrationRunning, Phase: vmmigration.PhaseCopying,
		Intervals: []vmmigration.IntervalProgress{
			{Sequence: 1, DurationSeconds: 42, BytesTransferred: 1024, TotalBytes: 1024, Completed: true},
			{Sequence: 2, BytesTransferred: 256, TotalBytes: 1024, ThroughputMiBps: 64},
		},
	}}
	server := newMigrationTestServer(t, stub)

	recorder := do(t, server, http.MethodGet, "/v1/migrations/mig-1", "", http.StatusOK)
	var response migrationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.BytesTransferred != 1280 || len(response.Snapshots) != 2 {
		t.Fatalf("response = %+v", response)
	}
	if !response.Snapshots[0].Completed || response.Snapshots[0].DurationSeconds != 42 {
		t.Fatalf("snapshot 0 = %+v", response.Snapshots[0])
	}
	// The throttle step is visible per interval, so an operator sees it decrease.
	if response.Snapshots[1].ThroughputMiBps != 64 {
		t.Fatalf("snapshot 1 throughput = %d, want 64", response.Snapshots[1].ThroughputMiBps)
	}
}

func TestPrepareMigrationSourceLocksTheSource(t *testing.T) {
	stub := &stubMigrationManager{handshake: vmmigration.SourceHandshake{
		Config:        vmmigration.PortableConfig{VirtualMachineID: "vm-00001"},
		ObservedState: vm.StateRunning,
	}}
	server := newMigrationTestServer(t, stub)

	recorder := do(t, server, http.MethodPut, "/v1/migrations/mig-1/source?virtual_machine_id=vm-00001", "", http.StatusOK)

	if want := []string{"mig-1", "vm-00001"}; !equalStrings(stub.lockArgs, want) {
		t.Fatalf("lock args = %v", stub.lockArgs)
	}
	var response migrationSourceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Config.VirtualMachineID != "vm-00001" || response.ObservedState != vm.StateRunning {
		t.Fatalf("response = %+v", response)
	}
}

func TestPrepareMigrationSourceRequiresTheVirtualMachineID(t *testing.T) {
	server := newMigrationTestServer(t, &stubMigrationManager{})

	do(t, server, http.MethodPut, "/v1/migrations/mig-1/source", "", http.StatusBadRequest)
}

func TestCreateMigrationSnapshotReturnsTheNextSnapshot(t *testing.T) {
	stub := &stubMigrationManager{snapshot: vmmigration.SourceSnapshot{Sequence: 2, SizeBytes: 1048576, GUID: "g2"}}
	server := newMigrationTestServer(t, stub)

	recorder := do(t, server, http.MethodPost, "/v1/migrations/mig-1/snapshot?virtual_machine_id=vm-00001", `{"received_sequence":1}`, http.StatusOK)

	if stub.receivedSequence != 1 {
		t.Fatalf("received sequence = %d", stub.receivedSequence)
	}
	if want := []string{"mig-1", "vm-00001"}; !equalStrings(stub.snapshotArgs, want) {
		t.Fatalf("snapshot args = %v", stub.snapshotArgs)
	}
	var response snapshotResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Sequence != 2 || response.SizeBytes != 1048576 || response.GUID != "g2" {
		t.Fatalf("response = %+v", response)
	}
}

func TestCreateMigrationSnapshotAcceptsNoBody(t *testing.T) {
	stub := &stubMigrationManager{snapshot: vmmigration.SourceSnapshot{Sequence: 1}}
	server := newMigrationTestServer(t, stub)

	do(t, server, http.MethodPost, "/v1/migrations/mig-1/snapshot?virtual_machine_id=vm-00001", "", http.StatusOK)
	if stub.receivedSequence != 0 {
		t.Fatalf("received %d, want 0", stub.receivedSequence)
	}
}

func TestStopMigrationSourceReturnsFinalSnapshot(t *testing.T) {
	stub := &stubMigrationManager{snapshot: vmmigration.SourceSnapshot{Sequence: 3, SizeBytes: 2048, GUID: "final"}}
	server := newMigrationTestServer(t, stub)

	recorder := do(t, server, http.MethodPost, "/v1/migrations/mig-1/stop?virtual_machine_id=vm-00001", "", http.StatusOK)

	if want := []string{"mig-1", "vm-00001"}; !equalStrings(stub.stopArgs, want) {
		t.Fatalf("stop args = %v", stub.stopArgs)
	}
	var response snapshotResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Sequence != 3 || response.SizeBytes != 2048 || response.GUID != "final" {
		t.Fatalf("response = %+v", response)
	}
}

func TestFinishMigrationRecordsTheRequest(t *testing.T) {
	stub := &stubMigrationManager{}
	server := newMigrationTestServer(t, stub)

	do(t, server, http.MethodPost, "/v1/migrations/mig-1/finish", "", http.StatusAccepted)
	if stub.finishedID != "mig-1" {
		t.Fatalf("finished id = %q", stub.finishedID)
	}
}

func TestStartMigrationSourceRestores(t *testing.T) {
	stub := &stubMigrationManager{}
	server := newMigrationTestServer(t, stub)

	do(t, server, http.MethodPost, "/v1/migrations/mig-1/start?virtual_machine_id=vm-00001", "", http.StatusNoContent)
	if want := []string{"mig-1", "vm-00001"}; !equalStrings(stub.startArgs, want) {
		t.Fatalf("start args = %v", stub.startArgs)
	}
}

func TestDestroyMigrationSource(t *testing.T) {
	stub := &stubMigrationManager{}
	server := newMigrationTestServer(t, stub)

	do(t, server, http.MethodPost, "/v1/migrations/mig-1/destroy?virtual_machine_id=vm-00001", "", http.StatusNoContent)
	if want := []string{"mig-1", "vm-00001"}; !equalStrings(stub.destroyArgs, want) {
		t.Fatalf("destroy args = %v", stub.destroyArgs)
	}
}

func TestDeleteMigrationSourceUnlocks(t *testing.T) {
	stub := &stubMigrationManager{}
	server := newMigrationTestServer(t, stub)

	do(t, server, http.MethodDelete, "/v1/migrations/mig-1?virtual_machine_id=vm-00001", "", http.StatusNoContent)
	if want := []string{"mig-1", "vm-00001"}; !equalStrings(stub.unlockArgs, want) {
		t.Fatalf("unlock args = %v", stub.unlockArgs)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
