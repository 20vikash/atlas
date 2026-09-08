//go:build integration

// Boots real microVMs over SSH. Requires root, KVM, Firecracker, Jailer, and a prepared host.
//
//	sudo -E go test -tags integration -v ./internal/firecracker/
//
// Tests run one at a time and clean up their VMs.
package firecracker

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/frappe/atlas/metal/internal/console"
	"github.com/frappe/atlas/metal/internal/network"
	"github.com/frappe/atlas/metal/internal/network/activity"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// skipUnlessHost skips when the host prerequisites are missing.
func skipUnlessHost(t *testing.T) (image, pub string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root (jailer)")
	}
	image = os.Getenv("METAL_IMAGE")
	pubPath := os.Getenv("METAL_SSH_PUB")
	if image == "" || pubPath == "" || os.Getenv("METAL_SSH_KEY") == "" ||
		os.Getenv("METAL_IMAGE_URL") == "" || os.Getenv("METAL_IMAGE_SHA256") == "" ||
		os.Getenv("METAL_KERNEL_URL") == "" || os.Getenv("METAL_KERNEL_SHA256") == "" {
		t.Skip("set the METAL image, kernel, and SSH environment variables")
	}
	b, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	return image, strings.TrimSpace(string(b))
}

// integrationMesh builds the mesh required by the host test.
func integrationMesh(t *testing.T) *network.Mesh {
	t.Helper()
	mesh, err := network.NewMesh(network.MeshConfig{
		CommandPath:   "atlas-wg-mesh",
		UplinkName:    "eth0",
		WireGuardName: "wg0",
	})
	if err != nil {
		t.Skipf("Atlas WG Mesh is not installed: %v", err)
	}
	return mesh
}

func newManager(t *testing.T) *vm.Manager {
	t.Helper()
	units, err := platform.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { units.Close() })
	stores := storage.NewStores(t.Context(), env("METAL_POOL", "metal"), env("METAL_IMAGES_DIR", "/var/lib/metal/images"), nil)
	serialBroker := console.NewSerialBroker(t.TempDir(), platform.NewFileDescriptorStore())
	t.Cleanup(serialBroker.Shutdown)
	activityMonitor, err := activity.NewMonitor(activity.MonitorConfig{UserIDRange: vm.DefaultUserIDRange})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activityMonitor.Close() })
	networkManager := network.NewLinuxAllocator(integrationMesh(t), activityMonitor)
	virtualMachineRuntime := NewRuntime(
		DefaultConfig(),
		units,
		stores.VirtualMachines,
		stores.Images,
		serialBroker,
		nil,
	)
	manager, err := vm.NewManager(vm.ManagerConfig{
		MachinesDirectory: DefaultConfig().MachinesDir,
	}, vm.ManagerDependencies{
		Runtime: virtualMachineRuntime, Network: networkManager,
		Storage: stores.VirtualMachines, Snapshots: stores.Snapshots,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func spec(image, pub string) vm.Specification {
	return vm.Specification{
		VirtualCPUCount: 1, MemoryMiB: 256, DiskMiB: 1024,
		Image: vm.Image{
			Name:         image,
			RootfsURL:    os.Getenv("METAL_IMAGE_URL"),
			RootfsSHA256: os.Getenv("METAL_IMAGE_SHA256"),
			KernelURL:    os.Getenv("METAL_KERNEL_URL"),
			KernelSHA256: os.Getenv("METAL_KERNEL_SHA256"),
			Architecture: runtime.GOARCH,
		},
		Network: vm.NetworkConfiguration{Egress: vm.EgressUplink},
		SSHKeys: []string{pub},
	}
}

// sshCmd runs one command in the VM's netns over SSH, returning trimmed stdout.
func sshCmd(id, cmd string) (string, error) {
	out, err := exec.CommandContext(context.Background(), "ip", "netns", "exec", "metal-"+id,
		"ssh", "-i", os.Getenv("METAL_SSH_KEY"),
		"-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=3",
		env("METAL_SSH_USER", "root")+"@172.16.0.2", cmd).Output()
	return strings.TrimSpace(string(out)), err
}

// run runs a command over SSH and fails the test if it errors.
func run(t *testing.T, id, cmd string) string {
	t.Helper()
	out, err := sshCmd(id, cmd)
	if err != nil {
		t.Fatalf("ssh %q: %v", cmd, err)
	}
	return out
}

// waitSSH waits for the guest to accept SSH.
func waitSSH(t *testing.T, id string) bool {
	t.Helper()
	for range 30 {
		if out, err := sshCmd(id, "echo ok"); err == nil && out == "ok" {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// bootVM creates a VM, starts it, waits for SSH, and registers cleanup.
func bootVM(t *testing.T, manager *vm.Manager, specification vm.Specification) string {
	t.Helper()
	identifier := uuid.NewString()
	_, err := manager.Create(context.Background(), identifier, specification, vm.SleepPolicy{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.Delete(context.Background(), identifier)
		_ = manager.Reconcile(context.Background(), identifier)
	})
	if err := manager.Reconcile(context.Background(), identifier); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitSSH(t, identifier) {
		t.Fatalf("ssh into %s never succeeded", identifier)
	}
	return identifier
}

func TestBootAndSSH(t *testing.T) {
	image, pub := skipUnlessHost(t)
	bootVM(t, newManager(t), spec(image, pub))
}

// mainPID returns the systemd MainPID of a VM unit, or 0 when it is not running.
func mainPID(t *testing.T, id string) int {
	t.Helper()
	out, err := exec.CommandContext(context.Background(),
		"systemctl", "show", "metal-vm@"+id+".service", "-p", "MainPID", "--value").Output()
	if err != nil {
		t.Fatalf("systemctl show %s: %v", id, err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return pid
}

// TestWarmStopAndRestoreTwoCycles checks guest memory across two warm-stop cycles.
func TestWarmStopAndRestoreTwoCycles(t *testing.T) {
	image, pub := skipUnlessHost(t)
	manager := newManager(t)
	id := bootVM(t, manager, spec(image, pub))

	token := uuid.NewString()
	run(t, id, "echo "+token+" > /dev/shm/token")
	guestPID := run(t, id, "sh -c 'nohup sleep 100000 >/dev/null 2>&1 & echo $!'")

	for cycle := 1; cycle <= 2; cycle++ {
		before := mainPID(t, id)
		if before == 0 {
			t.Fatalf("cycle %d: no Firecracker process before warm stop", cycle)
		}

		if err := manager.StopWarm(context.Background(), id); err != nil {
			t.Fatalf("cycle %d: warm stop request: %v", cycle, err)
		}
		if err := manager.Reconcile(context.Background(), id); err != nil {
			t.Fatalf("cycle %d: warm stop reconcile: %v", cycle, err)
		}
		if pid := mainPID(t, id); pid != 0 {
			t.Fatalf("cycle %d: Firecracker still running (pid %d) after warm stop", cycle, pid)
		}

		if err := manager.SetPowerState(context.Background(), id, vm.StateRunning); err != nil {
			t.Fatalf("cycle %d: start request: %v", cycle, err)
		}
		if err := manager.Reconcile(context.Background(), id); err != nil {
			t.Fatalf("cycle %d: restore reconcile: %v", cycle, err)
		}
		if !waitSSH(t, id) {
			t.Fatalf("cycle %d: ssh never returned after restore", cycle)
		}

		after := mainPID(t, id)
		if after == 0 || after == before {
			t.Fatalf("cycle %d: want a new Firecracker pid, before %d after %d", cycle, before, after)
		}
		if got := run(t, id, "cat /dev/shm/token"); got != token {
			t.Fatalf("cycle %d: token = %q, want %q, so memory was not restored", cycle, got, token)
		}
		if _, err := sshCmd(id, "kill -0 "+guestPID); err != nil {
			t.Fatalf("cycle %d: long-running process %s is gone: %v", cycle, guestPID, err)
		}
		if _, err := os.Stat(DefaultConfig().memorySnapshotRoot(id)); !os.IsNotExist(err) {
			t.Fatalf("cycle %d: external snapshots were not removed after restore: %v", cycle, err)
		}
	}
}
