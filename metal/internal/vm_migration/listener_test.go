package vmmigration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

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
