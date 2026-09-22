package network

import (
	"context"
	"path/filepath"
	"testing"
)

type testUnicastSpawner struct {
	starts    int
	processes []*testUnicastProcess
}

func (spawner *testUnicastSpawner) Start(UnicastConfig) (unicastProcess, error) {
	spawner.starts++
	process := &testUnicastProcess{}
	spawner.processes = append(spawner.processes, process)
	return process, nil
}

type testUnicastProcess struct {
	exited     bool
	terminated bool
}

func (process *testUnicastProcess) Exited() bool {
	return process.exited
}

func (process *testUnicastProcess) Terminate(context.Context) error {
	process.terminated = true
	process.exited = true
	return nil
}

func newTestUnicastManager(t *testing.T, peers []WireGuardPeer) (*UnicastManager, *testUnicastSpawner) {
	t.Helper()

	statePath := filepath.Join(t.TempDir(), "wireguard-peers.json")
	if peers != nil {
		if err := saveWireGuardPeers(statePath, peers); err != nil {
			t.Fatal(err)
		}
	}

	manager, err := NewUnicastManager(UnicastConfig{
		BinaryPath:         "/usr/local/bin/atlas-wg-mesh",
		WireGuardStatePath: statePath,
	})
	if err != nil {
		t.Fatal(err)
	}

	spawner := &testUnicastSpawner{}
	manager.spawner = spawner
	manager.startupGrace = 0
	return manager, spawner
}

var testDaemonPeers = []WireGuardPeer{{
	Node:        "server-11",
	MeshAddress: "fdab:1::11",
	PublicKey:   "key11",
	Address:     "10.20.0.11:7373",
	MAC:         "aa:bb:cc:dd:ee:11",
}}

func TestEnableStartsTheDaemonWhenPeersExist(t *testing.T) {
	manager, spawner := newTestUnicastManager(t, testDaemonPeers)

	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if spawner.starts != 1 {
		t.Fatalf("daemon starts = %d, want 1", spawner.starts)
	}
}

func TestEnableLeavesARunningDaemonAlone(t *testing.T) {
	manager, spawner := newTestUnicastManager(t, testDaemonPeers)

	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if spawner.starts != 1 {
		t.Fatalf("daemon starts = %d, want 1", spawner.starts)
	}
}

func TestEnableRestartsACrashedDaemon(t *testing.T) {
	manager, spawner := newTestUnicastManager(t, testDaemonPeers)

	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	spawner.processes[0].exited = true

	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if spawner.starts != 2 {
		t.Fatalf("daemon starts = %d, want 2", spawner.starts)
	}
}

func TestDisableStopsTheDaemon(t *testing.T) {
	manager, spawner := newTestUnicastManager(t, testDaemonPeers)

	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Disable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !spawner.processes[0].terminated {
		t.Fatal("the daemon was not stopped")
	}

	if err := manager.Disable(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestNewUnicastManagerRequiresConfiguration(t *testing.T) {
	if _, err := NewUnicastManager(UnicastConfig{WireGuardStatePath: "/tmp/peers.json"}); err == nil {
		t.Fatal("expected an error for a missing binary path")
	}
	if _, err := NewUnicastManager(UnicastConfig{BinaryPath: "/usr/local/bin/atlas-wg-mesh"}); err == nil {
		t.Fatal("expected an error for a missing peer state path")
	}
}
