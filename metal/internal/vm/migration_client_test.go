package vm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareSourceReadsTheHandshake(t *testing.T) {
	var gotAuthorization, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotPath = r.Method + " " + r.URL.Path
		w.Write([]byte(`{"config":{"virtual_machine_id":"vm-1","specification":{"memory_mib":2048}},"observed_state":"running"}`))
	}))
	defer server.Close()

	config, state, err := NewHTTPSourceClient(0).PrepareSource(context.Background(), server.URL, "mig-1", "vm-1", "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "Bearer tok-1" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if gotPath != "PUT /v1/migrations/mig-1/source" {
		t.Fatalf("path = %q", gotPath)
	}
	if config.VirtualMachineID != "vm-1" || config.Specification.MemoryMiB != 2048 || state != StateRunning {
		t.Fatalf("config = %+v state = %s", config, state)
	}
}

func TestRemoveSourceUsesTheMigrationPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := NewHTTPSourceClient(0).RemoveSource(context.Background(), server.URL, "mig-1", "tok-1"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "DELETE /v1/migrations/mig-1" {
		t.Fatalf("path = %q", gotPath)
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

	snapshot, err := NewHTTPSourceClient(0).NextSnapshot(context.Background(), server.URL, "mig-1", "tok-1", 1)
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

func TestStreamSnapshotCopiesTheResponse(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(make([]byte, 4096))
	}))
	defer server.Close()

	var sink bytes.Buffer
	written, err := NewHTTPSourceClient(0).StreamSnapshot(context.Background(), server.URL, "mig-1", "tok-1", 2, "resume-abc", &sink)
	if err != nil {
		t.Fatal(err)
	}
	if written != 4096 || sink.Len() != 4096 {
		t.Fatalf("written = %d, buffered = %d", written, sink.Len())
	}
	if !strings.Contains(gotBody, `"sequence":2`) || !strings.Contains(gotBody, `"resume_token":"resume-abc"`) {
		t.Fatalf("body = %q", gotBody)
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
	client := NewHTTPSourceClient(0)

	if _, _, err := client.PrepareSource(context.Background(), server.URL, "gone", "vm-1", "tok-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source = %v, want ErrNotFound", err)
	}
	if _, _, err := client.PrepareSource(context.Background(), server.URL, "mig-1", "vm-1", "tok-1"); err == nil {
		t.Fatal("want an error for HTTP 500")
	}
}
