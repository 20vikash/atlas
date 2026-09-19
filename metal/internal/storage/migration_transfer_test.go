package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

type fakeRunner struct {
	runErr   map[string]error
	outputs  map[string]string
	outErr   map[string]error
	combined map[string]string
	calls    []string
}

func commandKey(args ...string) string { return strings.Join(args, " ") }

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) error {
	f.calls = append(f.calls, commandKey(args...))
	return f.runErr[commandKey(args...)]
}

func (f *fakeRunner) Output(_ context.Context, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, commandKey(args...))
	key := commandKey(args...)
	return f.outputs[key], f.outErr[key]
}

func (f *fakeRunner) CombinedOutput(_ context.Context, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, commandKey(args...))
	return f.combined[commandKey(args...)], nil
}

func newTransfer(runner platform.Runner) *MigrationTransfer {
	return &MigrationTransfer{pool: &ZFSPool{name: "metal"}, runner: runner}
}

func TestSendArgumentsFormsFullIncrementalAndResume(t *testing.T) {
	transfer := newTransfer(&fakeRunner{})

	full := transfer.sendArguments("vm-1", "migration-m1-2", "", "", false)
	if commandKey(full...) != "send metal/vms/vm-1@migration-m1-2" {
		t.Fatalf("full = %v", full)
	}
	incremental := transfer.sendArguments("vm-1", "migration-m1-2", "migration-m1-1", "", false)
	if commandKey(incremental...) != "send -i metal/vms/vm-1@migration-m1-1 metal/vms/vm-1@migration-m1-2" {
		t.Fatalf("incremental = %v", incremental)
	}
	resume := transfer.sendArguments("vm-1", "migration-m1-2", "migration-m1-1", "token-xyz", false)
	if commandKey(resume...) != "send -t token-xyz" {
		t.Fatalf("resume = %v", resume)
	}
	estimate := transfer.sendArguments("vm-1", "migration-m1-2", "migration-m1-1", "", true)
	if commandKey(estimate...) != "send -nP -i metal/vms/vm-1@migration-m1-1 metal/vms/vm-1@migration-m1-2" {
		t.Fatalf("estimate = %v", estimate)
	}
}

func TestOpenSSLArgumentsRequireMutualTLSAndVerifyTheSourceIP(t *testing.T) {
	transfer := &MigrationTransfer{tls: MigrationTLSConfig{
		CAFile: "/tls/ca.crt", CertificateFile: "/tls/node.crt", PrivateKeyFile: "/tls/node.key",
		ListenAddress: "[fdab::12]:9002", TransferPort: 9002,
	}}

	server := commandKey(transfer.serverArguments()...)
	for _, required := range []string{
		"-naccept 2", "-accept [fdab::12]:9002", "-cert /tls/node.crt", "-key /tls/node.key",
		"-verifyCAfile /tls/ca.crt", "-Verify 1", "-verify_return_error", "-tls1_3",
	} {
		if !strings.Contains(server, required) {
			t.Fatalf("server arguments %q do not contain %q", server, required)
		}
	}

	client := commandKey(transfer.clientArguments("fdab::11", false)...)
	for _, required := range []string{
		"-connect [fdab::11]:9002", "-cert /tls/node.crt", "-key /tls/node.key",
		"-verifyCAfile /tls/ca.crt", "-verify_return_error", "-verify_ip fdab::11", "-tls1_3",
	} {
		if !strings.Contains(client, required) {
			t.Fatalf("client arguments %q do not contain %q", client, required)
		}
	}
	if !strings.Contains(client, "-ign_eof") || strings.Contains(client, "-no_ign_eof") {
		t.Fatalf("data client %q must read until the source closes", client)
	}

	probe := commandKey(transfer.clientArguments("fdab::11", true)...)
	if !strings.Contains(probe, "-no_ign_eof") {
		t.Fatalf("readiness probe %q must stop at its own input EOF", probe)
	}
}

func TestMigrationHostUsesTheCoordinationURLHost(t *testing.T) {
	host, err := migrationHost("https://[fdab::11]:9001")
	if err != nil {
		t.Fatal(err)
	}
	if host != "fdab::11" {
		t.Fatalf("host = %q, want fdab::11", host)
	}
}

func TestCreateSnapshotIgnoresAlreadyExists(t *testing.T) {
	runner := &fakeRunner{runErr: map[string]error{
		"snapshot metal/vms/vm-1@migration-m1-1": errors.New("cannot create snapshot: dataset already exists"),
	}}
	if err := newTransfer(runner).CreateSnapshot(context.Background(), "vm-1", "migration-m1-1"); err != nil {
		t.Fatalf("create existing snapshot = %v, want nil", err)
	}
}

func TestRemoveSnapshotIgnoresMissing(t *testing.T) {
	runner := &fakeRunner{runErr: map[string]error{
		"destroy metal/vms/vm-1@migration-m1-1": errors.New("could not find any snapshots to destroy; dataset does not exist"),
	}}
	if err := newTransfer(runner).RemoveSnapshot(context.Background(), "vm-1", "migration-m1-1"); err != nil {
		t.Fatalf("remove missing snapshot = %v, want nil", err)
	}
}

func TestAbortReceiveIgnoresNoResumableStateAndRemovesTheDataset(t *testing.T) {
	runner := &fakeRunner{runErr: map[string]error{
		"recv -A metal/vms/vm-1": errors.New("'metal/vms/vm-1' does not have any resumable receive state to abort"),
	}}
	if err := newTransfer(runner).AbortReceive(context.Background(), "vm-1"); err != nil {
		t.Fatalf("abort receive = %v, want nil", err)
	}
	if got := strings.Join(runner.calls, "|"); got != "recv -A metal/vms/vm-1|destroy -r metal/vms/vm-1" {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestAbortReceiveIgnoresAMissingDataset(t *testing.T) {
	runner := &fakeRunner{runErr: map[string]error{
		"recv -A metal/vms/vm-1":    errors.New("cannot open 'metal/vms/vm-1': dataset does not exist"),
		"destroy -r metal/vms/vm-1": errors.New("cannot open 'metal/vms/vm-1': dataset does not exist"),
	}}
	if err := newTransfer(runner).AbortReceive(context.Background(), "vm-1"); err != nil {
		t.Fatalf("abort receive on a missing dataset = %v, want nil", err)
	}
}

func TestSnapshotGUIDReadsTheValue(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		"get -Hp -o value guid metal/vms/vm-1@migration-m1-1": "12345678901234567890\n",
	}}
	guid, err := newTransfer(runner).SnapshotGUID(context.Background(), "vm-1", "migration-m1-1")
	if err != nil {
		t.Fatal(err)
	}
	if guid != "12345678901234567890" {
		t.Fatalf("guid = %q", guid)
	}
}

func TestEstimateStreamBytesParsesFullAndIncremental(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		"send -nP metal/vms/vm-1@migration-m1-1":                                  "full\tmetal/vms/vm-1@migration-m1-1\t268435456\nsize\t268435456\n",
		"send -nP -i metal/vms/vm-1@migration-m1-1 metal/vms/vm-1@migration-m1-2": "incremental\tmigration-m1-1\tmetal/vms/vm-1@migration-m1-2\t1048576\nsize\t1048576\n",
	}}
	transfer := newTransfer(runner)

	full, err := transfer.EstimateStreamBytes(context.Background(), "vm-1", "migration-m1-1", "")
	if err != nil || full != 268435456 {
		t.Fatalf("full estimate = %d, %v", full, err)
	}
	incremental, err := transfer.EstimateStreamBytes(context.Background(), "vm-1", "migration-m1-2", "migration-m1-1")
	if err != nil || incremental != 1048576 {
		t.Fatalf("incremental estimate = %d, %v", incremental, err)
	}
}

func TestReceiveResumeTokenHandlesDashAndMissing(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string]string{"get -Hp -o value receive_resume_token metal/vms/vm-1": "-\n"},
	}
	token, err := newTransfer(runner).ReceiveResumeToken(context.Background(), "vm-1")
	if err != nil || token != "" {
		t.Fatalf("dash token = %q, %v", token, err)
	}

	runner = &fakeRunner{outErr: map[string]error{
		"get -Hp -o value receive_resume_token metal/vms/vm-2": errors.New("dataset does not exist"),
	}}
	token, err = newTransfer(runner).ReceiveResumeToken(context.Background(), "vm-2")
	if err != nil || token != "" {
		t.Fatalf("missing dataset token = %q, %v", token, err)
	}
}

func TestTargetDatasetExists(t *testing.T) {
	runner := &fakeRunner{}
	present, err := newTransfer(runner).TargetDatasetExists(context.Background(), "vm-1")
	if err != nil || !present {
		t.Fatalf("present = %v, %v", present, err)
	}

	runner = &fakeRunner{runErr: map[string]error{
		"list metal/vms/vm-2": errors.New("dataset does not exist"),
	}}
	present, err = newTransfer(runner).TargetDatasetExists(context.Background(), "vm-2")
	if err != nil || present {
		t.Fatalf("absent = %v, %v", present, err)
	}
}

func TestVerifyResumeTokenChecksTheTargetSnapshot(t *testing.T) {
	runner := &fakeRunner{combined: map[string]string{
		"send -nvt good": "resume token contents:\n\ttoname = metal/vms/vm-1@migration-m1-2\n",
		"send -nvt bad":  "resume token contents:\n\ttoname = metal/vms/other-vm@migration-x-1\n",
	}}
	transfer := newTransfer(runner)

	if err := transfer.verifyResumeToken(context.Background(), "vm-1", "migration-m1-2", "good"); err != nil {
		t.Fatalf("matching token = %v, want nil", err)
	}
	if err := transfer.verifyResumeToken(context.Background(), "vm-1", "migration-m1-2", "bad"); err == nil {
		t.Fatal("mismatched token = nil, want an error")
	}
}

func TestParseSendSizeBytesRejectsMissingSize(t *testing.T) {
	if _, err := parseSendSizeBytes("full\tsnap\t10\n"); err == nil {
		t.Fatal("want an error when no size line is present")
	}
}
