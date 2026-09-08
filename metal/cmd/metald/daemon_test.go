package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeActivityMonitor records how the daemon closes the activity monitor. It
// captures whether the worker finished before Close ran.
type fakeActivityMonitor struct {
	closeError     error
	closed         bool
	workerFinished *atomic.Bool
	sawWorkerDone  bool
}

func (monitor *fakeActivityMonitor) Close() error {
	monitor.closed = true
	if monitor.workerFinished != nil {
		monitor.sawWorkerDone = monitor.workerFinished.Load()
	}
	return monitor.closeError
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeSnapshotUploadOwner struct {
	shutdown bool
}

func (owner *fakeSnapshotUploadOwner) Shutdown(context.Context) error {
	owner.shutdown = true
	return nil
}

type fakeSerialBroker struct {
	shutdown bool
}

func (owner *fakeSerialBroker) Shutdown() {
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
	serialBroker := &fakeSerialBroker{}
	systemd := &fakeSystemdConnection{}
	lifecycle := newDaemon(
		daemonContext,
		cancelDaemon,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		snapshotUploads,
		serialBroker,
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
	if !snapshotUploads.shutdown || !serialBroker.shutdown || !systemd.closed {
		t.Fatalf("owners were not stopped: uploads=%t consoles=%t systemd=%t", snapshotUploads.shutdown, serialBroker.shutdown, systemd.closed)
	}
}

func TestDaemonShutdownClosesTheActivityMonitorAfterWorkers(t *testing.T) {
	daemonContext, cancelDaemon := context.WithCancel(t.Context())
	workerFinished := &atomic.Bool{}
	monitor := &fakeActivityMonitor{workerFinished: workerFinished}
	lifecycle := newDaemon(daemonContext, cancelDaemon, discardLogger(),
		&fakeSnapshotUploadOwner{}, &fakeSerialBroker{}, &fakeSystemdConnection{})
	lifecycle.OwnActivityMonitor(monitor)
	lifecycle.StartWorker(func(workerContext context.Context) {
		<-workerContext.Done()
		workerFinished.Store(true)
	})

	if err := lifecycle.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !monitor.closed {
		t.Fatal("the activity monitor was not closed")
	}
	if !monitor.sawWorkerDone {
		t.Fatal("the activity monitor closed before the worker finished")
	}
}

func TestDaemonShutdownReportsAnActivityMonitorCloseError(t *testing.T) {
	daemonContext, cancelDaemon := context.WithCancel(t.Context())
	monitor := &fakeActivityMonitor{closeError: errors.New("map still busy")}
	lifecycle := newDaemon(daemonContext, cancelDaemon, discardLogger(),
		&fakeSnapshotUploadOwner{}, &fakeSerialBroker{}, &fakeSystemdConnection{})
	lifecycle.OwnActivityMonitor(monitor)

	err := lifecycle.Shutdown(t.Context())
	if err == nil || !strings.Contains(err.Error(), "activity monitor") {
		t.Fatalf("error = %v, want the activity monitor context", err)
	}
}

func TestDaemonShutdownWithoutAnActivityMonitor(t *testing.T) {
	daemonContext, cancelDaemon := context.WithCancel(t.Context())
	lifecycle := newDaemon(daemonContext, cancelDaemon, discardLogger(),
		&fakeSnapshotUploadOwner{}, &fakeSerialBroker{}, &fakeSystemdConnection{})

	if err := lifecycle.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown without a monitor returned %v", err)
	}
}

func TestDaemonShutdownWaitIsBounded(t *testing.T) {
	daemonContext, cancelDaemon := context.WithCancel(t.Context())
	lifecycle := newDaemon(
		daemonContext,
		cancelDaemon,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeSnapshotUploadOwner{},
		&fakeSerialBroker{},
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
