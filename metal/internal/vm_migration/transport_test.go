package vmmigration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

func TestPrepareSourceReadsTheHandshake(t *testing.T) {
	var gotVirtualMachineID, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVirtualMachineID = r.URL.Query().Get("virtual_machine_id")
		gotPath = r.Method + " " + r.URL.Path
		w.Write([]byte(`{"config":{"virtual_machine_id":"vm-1","specification":{"memory_mib":2048}},"observed_state":"running"}`))
	}))
	defer server.Close()

	config, state, err := NewSourceClient(0, 0).PrepareSource(context.Background(), server.URL, "mig-1", "vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotVirtualMachineID != "vm-1" {
		t.Fatalf("virtual_machine_id = %q", gotVirtualMachineID)
	}
	if gotPath != "PUT /v1/migrations/mig-1/source" {
		t.Fatalf("path = %q", gotPath)
	}
	if config.VirtualMachineID != "vm-1" || config.Specification.MemoryMiB != 2048 || state != vm.StateRunning {
		t.Fatalf("config = %+v state = %s", config, state)
	}
}

func TestRemoveSourceUsesTheMigrationPath(t *testing.T) {
	var gotPath, gotVirtualMachineID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotVirtualMachineID = r.URL.Query().Get("virtual_machine_id")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := NewSourceClient(0, 0).RemoveSource(context.Background(), server.URL, "mig-1", "vm-1"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "DELETE /v1/migrations/mig-1" || gotVirtualMachineID != "vm-1" {
		t.Fatalf("path = %q vm = %q", gotPath, gotVirtualMachineID)
	}
}

func TestNextSnapshotSendsTheAcknowledgedSequence(t *testing.T) {
	var gotBody, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Write([]byte(`{"sequence":2,"size_bytes":1048576,"guid":"g2"}`))
	}))
	defer server.Close()

	snapshot, err := NewSourceClient(0, 0).NextSnapshot(context.Background(), server.URL, "mig-1", "vm-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /v1/migrations/mig-1/snapshot" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"received_sequence":1`) {
		t.Fatalf("body = %q", gotBody)
	}
	if snapshot.Sequence != 2 || snapshot.SizeBytes != 1048576 || snapshot.GUID != "g2" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestStopSourceReturnsFinalSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/migrations/mig-1/stop" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sequence":3,"size_bytes":2048,"guid":"final"}`))
	}))
	defer server.Close()

	snapshot, err := NewSourceClient(0, 0).StopSource(context.Background(), server.URL, "mig-1", "vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sequence != 3 || snapshot.SizeBytes != 2048 || snapshot.GUID != "final" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestStartAndFinishSourcePost(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewSourceClient(0, 0)

	if err := client.StartSource(context.Background(), server.URL, "mig-1", "vm-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.FinishSource(context.Background(), server.URL, "mig-1", "vm-1"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "POST /v1/migrations/mig-1/start" || paths[1] != "POST /v1/migrations/mig-1/destroy" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSourceClientReportsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/migrations/gone/source" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewSourceClient(0, 0)

	if _, _, err := client.PrepareSource(context.Background(), server.URL, "gone", "vm-1"); !errors.Is(err, vm.ErrNotFound) {
		t.Fatalf("missing source = %v, want ErrNotFound", err)
	}
	if _, _, err := client.PrepareSource(context.Background(), server.URL, "mig-1", "vm-1"); err == nil {
		t.Fatal("want an error for HTTP 500")
	}
}

// fakeHandler records the request and writes a fixed stream.
type fakeHandler struct {
	migrationID      string
	virtualMachineID string
	sequence         int
	resumeToken      string
	throughputMiBps  int
	payload          []byte
	err              error
}

func (h *fakeHandler) SendSourceStream(_ context.Context, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	h.migrationID = migrationID
	h.virtualMachineID = virtualMachineID
	h.sequence = sequence
	h.resumeToken = resumeToken
	h.throughputMiBps = throughputMiBps
	if h.err != nil {
		return 0, h.err
	}
	written, err := w.Write(h.payload)
	return int64(written), err
}

// clientFor returns a client aimed at the listener's port.
func clientFor(t *testing.T, listener *SourceListener) *SourceClient {
	t.Helper()
	port := listener.Addr().(*net.TCPAddr).Port
	return NewSourceClient(0, port)
}

func TestStreamRoundTrip(t *testing.T) {
	handler := &fakeHandler{payload: bytes.Repeat([]byte{7}, 4096)}
	listener, err := Listen("127.0.0.1:0", handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Shutdown(shutdownContext(t))

	var sink bytes.Buffer
	written, err := clientFor(t, listener).StreamSnapshot(context.Background(), "http://127.0.0.1:9000", "mig-1", "vm-1", 2, "resume-abc", 32, &sink)
	if err != nil {
		t.Fatal(err)
	}
	if written != 4096 || sink.Len() != 4096 {
		t.Fatalf("written = %d, buffered = %d", written, sink.Len())
	}
	if handler.migrationID != "mig-1" || handler.virtualMachineID != "vm-1" || handler.sequence != 2 ||
		handler.resumeToken != "resume-abc" || handler.throughputMiBps != 32 {
		t.Fatalf("handler saw %+v", handler)
	}
}

func TestStreamReportsASourceRefusal(t *testing.T) {
	handler := &fakeHandler{err: errors.New("sequence mismatch")}
	listener, err := Listen("127.0.0.1:0", handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Shutdown(shutdownContext(t))

	var sink bytes.Buffer
	_, err = clientFor(t, listener).StreamSnapshot(context.Background(), "http://127.0.0.1:9000", "mig-1", "vm-1", 3, "", 0, &sink)
	if err == nil {
		t.Fatal("want an error when the source refuses the stream")
	}
	if sink.Len() != 0 {
		t.Fatalf("buffered %d bytes on a refusal", sink.Len())
	}
}

func TestStreamCancelsWithTheContext(t *testing.T) {
	release := make(chan struct{})
	handler := &blockingHandler{release: release}
	listener, err := Listen("127.0.0.1:0", handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Shutdown(shutdownContext(t))
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	var sink bytes.Buffer
	if _, err := clientFor(t, listener).StreamSnapshot(ctx, "http://127.0.0.1:9000", "mig-1", "vm-1", 1, "", 0, &sink); err == nil {
		t.Fatal("want an error when the context is canceled mid-stream")
	}
}

// blockingHandler starts a stream and then blocks until released.
type blockingHandler struct {
	release chan struct{}
}

func (h *blockingHandler) SendSourceStream(ctx context.Context, _, _ string, _ int, _ string, _ int, w io.Writer) (int64, error) {
	if _, err := w.Write([]byte{1}); err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 1, ctx.Err()
	case <-h.release:
		return 1, nil
	}
}

func shutdownContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
