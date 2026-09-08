package firecracker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/vm"
)

// TestWarmStopRejectsANonRunningVM checks the state guard before snapshotting.
func TestWarmStopRejectsANonRunningVM(t *testing.T) {
	units := &stubUnits{active: false} // inactive reports stopped
	m := testMachine(units, fcSocket(t, nil), time.Minute)

	if _, err := m.warmStop(context.Background()); !errors.Is(err, vm.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

// stubUnits models a systemd unit that stays active until stopped or killed.
type stubUnits struct {
	mu     sync.Mutex
	active bool
	failed bool
	stops  int
	kills  int
	waits  int
}

type stubSerialBroker struct{}

func (*stubSerialBroker) Open(string) error    { return nil }
func (*stubSerialBroker) Persist(string) error { return nil }
func (*stubSerialBroker) Close(string) error   { return nil }

func (s *stubUnits) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

func (s *stubUnits) Start(context.Context, string) error { return nil }

func (s *stubUnits) Stop(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	s.active = false
	return nil
}

func (s *stubUnits) Kill(context.Context, string, syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kills++
	s.active = false
	s.failed = true
	return nil
}

func (s *stubUnits) ResetFailed(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = false
	return nil
}

func (s *stubUnits) Status(context.Context, string) (platform.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.failed:
		return platform.Status{ActiveState: "failed"}, nil
	case s.active:
		return platform.Status{ActiveState: "active"}, nil
	}
	return platform.Status{ActiveState: "inactive"}, nil
}

func (s *stubUnits) Wait(ctx context.Context, _ string) (platform.Result, error) {
	s.mu.Lock()
	s.waits++
	s.mu.Unlock()
	for {
		s.mu.Lock()
		up := s.active
		s.mu.Unlock()
		if !up {
			return platform.Result{}, nil
		}
		select {
		case <-ctx.Done():
			return platform.Result{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (s *stubUnits) List(context.Context) ([]string, error)                   { return nil, nil }
func (s *stubUnits) SetLimits(context.Context, string, platform.Limits) error { return nil }

func (s *stubUnits) counts() (stops, kills, waits int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops, s.kills, s.waits
}

// fcSocket serves a Firecracker API socket and invokes onRequest.
func fcSocket(t *testing.T, onRequest func()) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "fc.socket")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if onRequest != nil {
			onRequest()
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func testMachine(units platform.UnitManager, sock string, timeout time.Duration) *machine {
	return &machine{
		runtime:     &Runtime{units: units, serialBroker: &stubSerialBroker{}},
		input:       vm.RuntimeMachine{ID: "abc"},
		api:         api.New(sock),
		stopTimeout: timeout,
	}
}

func TestStopKillsWhenGuestIgnoresCtrlAltDel(t *testing.T) {
	units := &stubUnits{active: true}
	m := testMachine(units, fcSocket(t, nil), 20*time.Millisecond)

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	stops, kills, _ := units.counts()
	if kills != 1 {
		t.Errorf("kills = %d, want 1", kills)
	}
	if stops != 0 {
		t.Errorf("systemd stops = %d, want 0", stops)
	}
}

func TestStopLetsGuestShutItselfDown(t *testing.T) {
	units := &stubUnits{active: true}
	m := testMachine(units, fcSocket(t, units.shutdown), time.Minute)

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	stops, kills, waits := units.counts()
	if stops != 0 || kills != 0 {
		t.Errorf("stops = %d, kills = %d, want 0 and 0", stops, kills)
	}
	if waits == 0 {
		t.Error("Stop did not wait for the guest to exit")
	}
}

func TestKillTerminates(t *testing.T) {
	units := &stubUnits{active: true}
	m := testMachine(units, fcSocket(t, nil), time.Minute)

	if err := m.kill(context.Background()); err != nil {
		t.Fatal(err)
	}
	stops, kills, waits := units.counts()
	if kills != 1 {
		t.Errorf("kills = %d, want 1", kills)
	}
	if stops != 0 {
		t.Errorf("systemd stops = %d, want 0", stops)
	}
	if waits == 0 {
		t.Error("kill did not wait for the process to exit")
	}
}

func TestStopClearsTheFailedUnitState(t *testing.T) {
	units := &stubUnits{active: true}
	m := testMachine(units, fcSocket(t, nil), 20*time.Millisecond)

	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := units.Status(context.Background(), m.input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, stateError := m.state(context.Background(), st); stateError != nil || got != vm.StateStopped {
		t.Errorf("state = %q, want %q", got, vm.StateStopped)
	}
}

func TestActiveVMWithUnreadableStateReturnsError(t *testing.T) {
	units := &stubUnits{active: true}
	machine := testMachine(units, fcSocket(t, nil), time.Minute)

	status, err := units.Status(context.Background(), machine.input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, stateError := machine.state(context.Background(), status); stateError == nil || got != vm.StateUnknown {
		t.Errorf("state = %q, error = %v", got, stateError)
	}
}

func TestCrashedVMReportsFailed(t *testing.T) {
	units := &stubUnits{failed: true}
	m := testMachine(units, fcSocket(t, nil), time.Minute)

	st, err := units.Status(context.Background(), m.input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, stateError := m.state(context.Background(), st); stateError != nil || got != vm.StateFailed {
		t.Errorf("state = %q, want %q", got, vm.StateFailed)
	}
}
