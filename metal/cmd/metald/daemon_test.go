package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeSnapshotUploadOwner struct {
	shutdown bool
}

func (owner *fakeSnapshotUploadOwner) Shutdown(context.Context) error {
	owner.shutdown = true
	return nil
}

type fakeConsoleSessionOwner struct {
	shutdown bool
}

func (owner *fakeConsoleSessionOwner) Shutdown() {
	owner.shutdown = true
}

type fakeSystemdConnection struct {
	closed bool
}

func (connection *fakeSystemdConnection) Close() {
	connection.closed = true
}

func TestDaemonShutdownOwnsBackgroundLifecycle(t *testing.T) {
	daemonContext, cancelDaemon := context.WithCancel(t.Context())
	snapshotUploads := &fakeSnapshotUploadOwner{}
	consoleSessions := &fakeConsoleSessionOwner{}
	systemd := &fakeSystemdConnection{}
	lifecycle := newDaemon(
		daemonContext,
		cancelDaemon,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		snapshotUploads,
		consoleSessions,
		systemd,
	)
	workerStopped := make(chan struct{})
	lifecycle.StartWorker(func(workerContext context.Context) {
		<-workerContext.Done()
		close(workerStopped)
	})

	if err := lifecycle.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	select {
	case <-workerStopped:
	default:
		t.Fatal("background worker did not stop")
	}
	if !snapshotUploads.shutdown || !consoleSessions.shutdown || !systemd.closed {
		t.Fatalf("owners were not stopped: uploads=%t consoles=%t systemd=%t", snapshotUploads.shutdown, consoleSessions.shutdown, systemd.closed)
	}
}

func TestDaemonShutdownWaitIsBounded(t *testing.T) {
	daemonContext, cancelDaemon := context.WithCancel(t.Context())
	lifecycle := newDaemon(
		daemonContext,
		cancelDaemon,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeSnapshotUploadOwner{},
		&fakeConsoleSessionOwner{},
		&fakeSystemdConnection{},
	)
	releaseWorker := make(chan struct{})
	lifecycle.StartWorker(func(context.Context) { <-releaseWorker })

	shutdownContext, cancelShutdown := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancelShutdown()
	if err := lifecycle.Shutdown(shutdownContext); err == nil {
		t.Fatal("shutdown did not report the worker timeout")
	}
	close(releaseWorker)
}
