package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frappe/atlas/metal/internal/host"
	"github.com/frappe/atlas/metal/internal/token"
	"github.com/frappe/atlas/metal/internal/vm"
)

type stubMigrationManager struct {
	record            vm.TargetMigrationRecord
	createErr         error
	statusErr         error
	abortErr          error
	handshake         vm.SourceHandshake
	lockErr           error
	unlockErr         error
	snapshot          vm.SourceSnapshot
	snapshotErr       error
	streamBytes       int64
	streamErr         error
	createArgs        []string
	lockArgs          []string
	unlockArgs        []string
	snapshotArgs      []string
	abortedID         string
	receivedSequence  int
	streamSequence    int
	streamResumeToken string
}

func (m *stubMigrationManager) CreateTarget(_ context.Context, migrationID, virtualMachineID, source, signedToken string) (vm.TargetMigrationRecord, error) {
	m.createArgs = []string{migrationID, virtualMachineID, source, signedToken}
	return m.record, m.createErr
}

func (m *stubMigrationManager) TargetStatus(context.Context, string) (vm.TargetMigrationRecord, error) {
	return m.record, m.statusErr
}

func (m *stubMigrationManager) AbortTarget(_ context.Context, migrationID string) error {
	m.abortedID = migrationID
	return m.abortErr
}

func (m *stubMigrationManager) LockSource(_ context.Context, migrationID, virtualMachineID, caller string) (vm.SourceHandshake, error) {
	m.lockArgs = []string{migrationID, virtualMachineID, caller}
	return m.handshake, m.lockErr
}

func (m *stubMigrationManager) NextSourceSnapshot(_ context.Context, migrationID, virtualMachineID, caller string, receivedSequence int) (vm.SourceSnapshot, error) {
	m.receivedSequence = receivedSequence
	m.snapshotArgs = []string{migrationID, virtualMachineID, caller}
	return m.snapshot, m.snapshotErr
}

func (m *stubMigrationManager) SendSourceStream(_ context.Context, migrationID, virtualMachineID, caller string, sequence int, resumeToken string, w io.Writer) (int64, error) {
	m.streamSequence = sequence
	m.streamResumeToken = resumeToken
	if m.streamErr != nil {
		return 0, m.streamErr
	}
	_, _ = w.Write(make([]byte, m.streamBytes))
	return m.streamBytes, nil
}

func (m *stubMigrationManager) UnlockSource(_ context.Context, migrationID, virtualMachineID, caller string) error {
	m.unlockArgs = []string{migrationID, virtualMachineID, caller}
	return m.unlockErr
}

func newMigrationTestServer(t *testing.T, migrations MigrationManager, trusted ...token.TrustedKeys) http.Handler {
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
		TrustedKeys:           newTrustedKeyStore(t, trusted...),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestCreateMigrationDrivesTheTarget(t *testing.T) {
	stub := &stubMigrationManager{record: vm.TargetMigrationRecord{ID: "mig-1", VirtualMachineID: "vm-1", Status: vm.MigrationRunning, Phase: vm.PhasePreparing}}
	server := newMigrationTestServer(t, stub)

	body := `{"virtual_machine_id":"vm-1","source":"http://10.0.0.3:9000","token":"tok-1"}`
	recorder := do(t, server, http.MethodPut, "/v1/migrations/mig-1", body, http.StatusAccepted)

	if want := []string{"mig-1", "vm-1", "http://10.0.0.3:9000", "tok-1"}; !equalStrings(stub.createArgs, want) {
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
	stub := &stubMigrationManager{record: vm.TargetMigrationRecord{ID: "mig-1", VirtualMachineID: "vm-1", Status: vm.MigrationReady, Phase: vm.PhaseCopying}}
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

func TestPrepareMigrationSourceUsesTheTokenClaims(t *testing.T) {
	signer := newAtlasSigner(t)
	stub := &stubMigrationManager{handshake: vm.SourceHandshake{
		Config:        vm.PortableConfig{VirtualMachineID: "vm-00001"},
		ObservedState: vm.StateRunning,
	}}
	server := newMigrationTestServer(t, stub, signer.trusted)

	request := httptest.NewRequest(http.MethodPut, "/v1/migrations/mig-1/source", nil)
	request.Header.Set("Authorization", "Bearer "+signer.sign(t, "vm-00001", token.ScopeReadVirtualMachine, token.ScopeMigration))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("prepare source = %d (%s)", recorder.Code, recorder.Body)
	}
	if want := []string{"mig-1", "vm-00001", testCaller}; !equalStrings(stub.lockArgs, want) {
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

func TestPrepareMigrationSourceRejectsTheStaticToken(t *testing.T) {
	signer := newAtlasSigner(t)
	server := newMigrationTestServer(t, &stubMigrationManager{}, signer.trusted)

	// The static token does not satisfy the JWT-only source route.
	do(t, server, http.MethodPut, "/v1/migrations/mig-1/source", "", http.StatusUnauthorized)
}

func TestCreateMigrationSnapshotUsesTheClaims(t *testing.T) {
	signer := newAtlasSigner(t)
	stub := &stubMigrationManager{snapshot: vm.SourceSnapshot{Sequence: 2, SizeBytes: 1048576, GUID: "g2"}}
	server := newMigrationTestServer(t, stub, signer.trusted)

	request := httptest.NewRequest(http.MethodPost, "/v1/migrations/mig-1/snapshot", strings.NewReader(`{"received_sequence":1}`))
	request.Header.Set("Authorization", "Bearer "+signer.sign(t, "vm-00001", token.ScopeMigration))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("snapshot = %d (%s)", recorder.Code, recorder.Body)
	}
	if stub.receivedSequence != 1 {
		t.Fatalf("received sequence = %d", stub.receivedSequence)
	}
	if want := []string{"mig-1", "vm-00001", testCaller}; !equalStrings(stub.snapshotArgs, want) {
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
	signer := newAtlasSigner(t)
	stub := &stubMigrationManager{snapshot: vm.SourceSnapshot{Sequence: 1}}
	server := newMigrationTestServer(t, stub, signer.trusted)

	request := httptest.NewRequest(http.MethodPost, "/v1/migrations/mig-1/snapshot", nil)
	request.Header.Set("Authorization", "Bearer "+signer.sign(t, "vm-00001", token.ScopeMigration))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || stub.receivedSequence != 0 {
		t.Fatalf("first snapshot = %d, received %d", recorder.Code, stub.receivedSequence)
	}
}

func TestStreamMigrationSnapshotStreamsBytes(t *testing.T) {
	signer := newAtlasSigner(t)
	stub := &stubMigrationManager{streamBytes: 4096}
	server := newMigrationTestServer(t, stub, signer.trusted)

	request := httptest.NewRequest(http.MethodPost, "/v1/migrations/mig-1/stream", strings.NewReader(`{"sequence":2,"resume_token":"rt"}`))
	request.Header.Set("Authorization", "Bearer "+signer.sign(t, "vm-00001", token.ScopeMigration))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("stream = %d (%s)", recorder.Code, recorder.Body)
	}
	if recorder.Body.Len() != 4096 || recorder.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("body = %d bytes, type %q", recorder.Body.Len(), recorder.Header().Get("Content-Type"))
	}
	if stub.streamSequence != 2 || stub.streamResumeToken != "rt" {
		t.Fatalf("stream args = %d %q", stub.streamSequence, stub.streamResumeToken)
	}
}

func TestDeleteMigrationSourceUnlocks(t *testing.T) {
	signer := newAtlasSigner(t)
	stub := &stubMigrationManager{}
	server := newMigrationTestServer(t, stub, signer.trusted)

	request := httptest.NewRequest(http.MethodDelete, "/v1/migrations/mig-1", nil)
	request.Header.Set("Authorization", "Bearer "+signer.sign(t, "vm-00001", token.ScopeMigration))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete source = %d (%s)", recorder.Code, recorder.Body)
	}
	if want := []string{"mig-1", "vm-00001", testCaller}; !equalStrings(stub.unlockArgs, want) {
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
