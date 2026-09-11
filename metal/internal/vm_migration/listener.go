package vmmigration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

// StreamHandler serves one disk stream for a validated request. It writes the
// zfs stream to w and returns the byte count. The source host implements it.
type StreamHandler interface {
	SendSourceStream(ctx context.Context, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error)
}

// SourceListener serves disk streams to target hosts over the mesh. It runs on
// the source host for the daemon lifetime.
type SourceListener struct {
	handler  StreamHandler
	listener net.Listener
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	wait     sync.WaitGroup
}

// Listen starts a source listener on the given TCP address.
func Listen(address string, handler StreamHandler, logger *slog.Logger) (*SourceListener, error) {
	if handler == nil {
		return nil, fmt.Errorf("stream handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for migration transfers on %s: %w", address, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	source := &SourceListener{handler: handler, listener: listener, logger: logger, ctx: ctx, cancel: cancel}
	source.wait.Add(1)
	go source.accept()
	return source, nil
}

// Addr returns the listener's network address.
func (s *SourceListener) Addr() net.Addr {
	return s.listener.Addr()
}

// accept serves each connection in its own goroutine until the listener closes.
func (s *SourceListener) accept() {
	defer s.wait.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wait.Add(1)
		go func() {
			defer s.wait.Done()
			s.serve(conn)
		}()
	}
}

// serve reads the request header and streams the requested snapshot.
func (s *SourceListener) serve(conn net.Conn) {
	defer conn.Close()

	stop := context.AfterFunc(s.ctx, func() { _ = conn.Close() })
	defer stop()

	header, err := readStreamHeader(conn)
	if err != nil {
		s.logger.Warn("read migration stream header", "error", err)
		return
	}

	status := &statusWriter{writer: conn}
	_, sendError := s.handler.SendSourceStream(s.ctx, header.MigrationID, header.VirtualMachineID, header.Sequence, header.ResumeToken, header.ThroughputMiBps, status)
	if sendError != nil {
		if status.started {
			s.logger.Error("migration stream failed after it started", "migration_id", header.MigrationID, "error", sendError)
			return
		}
		s.refuse(conn, sendError)
		return
	}
	if !status.started {
		// A zero-byte stream still signals success so the target proceeds.
		_, _ = conn.Write([]byte{streamStatusOK})
	}
}

// refuse reports a pre-stream failure to the target.
func (s *SourceListener) refuse(conn net.Conn, cause error) {
	if _, err := conn.Write([]byte{streamStatusError}); err != nil {
		return
	}
	message := []byte(cause.Error())
	if uint32(len(message)) > maxFrameBytes {
		message = message[:maxFrameBytes]
	}
	_ = writeFrame(conn, message)
}

// Shutdown stops the listener and waits for active streams to finish.
func (s *SourceListener) Shutdown(ctx context.Context) error {
	s.cancel()
	_ = s.listener.Close()

	done := make(chan struct{})
	go func() {
		s.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for migration listener: %w", ctx.Err())
	}
}
