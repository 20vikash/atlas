// Command metald is the metal daemon: an HTTP server (over a unix socket) that
// drives firecracker microVMs.
//
//	metald serve [--config path]   run the server (default)
//
// Use scripts/dev.sh to prepare a throwaway dev host before serve.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frappe/atlas/metal/internal/api"
	"github.com/frappe/atlas/metal/internal/console"
	"github.com/frappe/atlas/metal/internal/firecracker"
	"github.com/frappe/atlas/metal/internal/host"
	"github.com/frappe/atlas/metal/internal/network"
	platform "github.com/frappe/atlas/metal/internal/platform"
	"github.com/frappe/atlas/metal/internal/reconciler"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/vm"
)

const (
	reconcileInterval      = 5 * time.Second
	meshSetupTimeout       = 2 * time.Minute
	imageReconcileInterval = time.Hour
)

//	@title			Metal API
//	@version		1.0
//	@description	Metal manages Firecracker virtual machines and host resources.
//	@BasePath		/
//
//	@tag.name		Virtual machines
//	@tag.description	Manage desired and observed virtual machine state.
//	@tag.name		Snapshots
//	@tag.description	Stage and upload virtual machine image artifacts.
//	@tag.name		Host synchronization
//	@tag.description	Replace controller-owned host state and get capacity.
//	@tag.name		Health
//	@tag.description	Check the Metal HTTP server.
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Use "Bearer", one space, and the API token.

// version is the build version set with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	configPath, err := parseFlags(cmd, args)
	if err != nil {
		os.Exit(2)
	}
	if cmd != "serve" {
		fmt.Fprintf(os.Stderr, "usage: metald [serve] [--config path]\n")
		os.Exit(2)
	}
	o, err := load(configPath)
	if err == nil {
		err = serve(o, logger)
	}
	if err != nil {
		logger.Error("metald stopped", "error", err)
		os.Exit(1)
	}
}

func parseFlags(cmd string, args []string) (string, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	path := fs.String("config", "", "path to the configuration file (optional; defaults to "+defaultConfigPath+")")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	return *path, nil
}

func listen(addr string) (net.Listener, error) {
	if path, ok := strings.CutPrefix(addr, "unix:"); ok {
		_ = os.Remove(path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		ln, err := net.Listen("unix", path)
		if err == nil {
			_ = os.Chmod(path, 0o660)
		}
		return ln, err
	}
	return net.Listen("tcp", addr)
}

func makeDirs(o opts) error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{o.cfg.MachinesDir, 0o750},
		{o.cfg.SocketsDir, 0o700},
		{o.imagesDir, 0o755},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("create %s: %w", d.path, err)
		}
	}
	return nil
}

// meshProvider is the mesh behavior metald wires into the network allocator and
// the host service. A real Mesh or a DisabledMesh satisfies it.
type meshProvider interface {
	Add(ctx context.Context, address, interfaceName string) error
	Remove(ctx context.Context, address, interfaceName string) error
	ApplyPrivilegedAddresses(ctx context.Context, desired []string) error
}

// setUpMesh prepares the Atlas WG Mesh integration, or a disabled no-op mesh when
// the host turns it off. A disabled mesh gives VMs no mesh connectivity, so it is
// for development and testing only.
func setUpMesh(o opts, logger *slog.Logger) (meshProvider, error) {
	if !o.mesh.enabled {
		logger.Warn("Atlas WG Mesh is disabled; VMs have no mesh connectivity", "wg_mesh.enabled", false)
		return network.DisabledMesh{}, nil
	}

	return connectMesh(o)
}

// connectMesh prepares the Atlas WG Mesh integration. It configures the host on
// every start, so a reinstalled or reset host recovers without an operator.
func connectMesh(o opts) (*network.Mesh, error) {
	mesh, err := network.NewMesh(network.MeshConfig{
		CommandPath:   o.mesh.binaryPath,
		UplinkName:    o.mesh.uplinkName,
		WireGuardName: o.wireGuardName,
	})
	if err != nil {
		return nil, fmt.Errorf("configure Atlas WG Mesh: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), meshSetupTimeout)
	defer cancel()
	if err := mesh.EnsureHost(ctx); err != nil {
		return nil, fmt.Errorf("configure Atlas WG Mesh host: %w", err)
	}
	return mesh, nil
}

func serve(o opts, logger *slog.Logger) (serveError error) {
	if o.authTokenHash == "" {
		return fmt.Errorf("metald.auth_token_hash is required")
	}
	// Set up Atlas WG Mesh before metald builds anything, so a bad mesh fails fast.
	mesh, err := setUpMesh(o, logger)
	if err != nil {
		return err
	}
	if err := makeDirs(o); err != nil {
		return err
	}
	units, err := platform.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("connect systemd: %w", err)
	}

	daemonContext, cancelDaemon := context.WithCancel(context.Background())
	stores := storage.NewStores(daemonContext, o.pool, o.imagesDir, logger)
	serialBroker := console.NewSerialBroker(filepath.Join(o.cfg.SocketsDir, "consoles"))
	daemon := newDaemon(daemonContext, cancelDaemon, logger, stores.Snapshots, serialBroker, units)
	defer func() {
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancelShutdown()
		serveError = errors.Join(serveError, daemon.Shutdown(shutdownContext))
	}()

	wireGuardManager, err := network.NewWireGuardManager(network.WireGuardConfig{
		InterfaceName: o.wireGuardName,
		StatePath:     filepath.Join(o.baseDir, "wireguard-peers.json"),
	})
	if err != nil {
		return fmt.Errorf("configure WireGuard manager: %w", err)
	}
	activityMonitor, err := network.NewActivityMonitor(network.ActivityMonitorConfig{UserIDRange: vm.DefaultUserIDRange})
	if err != nil {
		return fmt.Errorf("configure activity monitor: %w", err)
	}
	daemon.OwnActivityMonitor(activityMonitor)
	networkManager := network.NewLinuxAllocator(mesh, activityMonitor)
	virtualMachineRuntime := firecracker.NewRuntime(
		o.cfg,
		units,
		stores.VirtualMachines,
		stores.Images,
		serialBroker,
		logger,
	)
	virtualMachineManager, err := vm.NewManager(
		vm.ManagerConfig{
			MachinesDirectory: o.cfg.MachinesDir,
			Sleep:             vm.SleepConfig{Enabled: o.sleep.enabled, IdleTimeout: o.sleep.idleTimeout},
		},
		vm.ManagerDependencies{
			Runtime:                virtualMachineRuntime,
			Network:                networkManager,
			Storage:                stores.VirtualMachines,
			Snapshots:              stores.Snapshots,
			NetworkActivityMonitor: networkManager,
			NetworkWakeMonitor:     activityMonitor,
			Logger:                 logger,
		},
	)
	if err != nil {
		return fmt.Errorf("configure VM manager: %w", err)
	}
	memorySnapshotBuilder := vm.NewWarmImageBuilder(
		virtualMachineManager,
		virtualMachineRuntime,
		stores.Images,
	)

	virtualMachineReconciler := reconciler.NewVirtualMachineReconciler(
		virtualMachineManager,
		reconcileInterval,
		reconciler.VirtualMachineConfig{Logger: logger},
	)
	imageReconciler := reconciler.NewImageReconciler(
		stores.Images,
		stores.Snapshots,
		memorySnapshotBuilder,
		imageReconcileInterval,
		reconciler.ImageConfig{Logger: logger},
	)
	wakeReconcilers := func() {
		virtualMachineReconciler.Wake()
		imageReconciler.Wake()
	}
	hostService, err := host.NewService(host.Dependencies{
		Mesh: mesh, WireGuard: wireGuardManager, Images: stores.Images,
		VirtualMachines: virtualMachineManager, Storage: stores.Pool, Wake: wakeReconcilers,
	})
	if err != nil {
		return fmt.Errorf("configure host service: %w", err)
	}
	server, err := api.New(api.Config{AuthTokenHash: o.authTokenHash, Logger: logger}, api.Dependencies{
		VirtualMachineManager: virtualMachineManager,
		SnapshotStore:         stores.Snapshots,
		WakeReconciler:        wakeReconcilers,
		HostService:           hostService,
		SerialBroker:          serialBroker,
	})
	if err != nil {
		return fmt.Errorf("configure API: %w", err)
	}

	listener, err := listen(o.listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", o.listen, err)
	}
	logger.Info("metald listening", "version", version, "address", o.listen)
	server.Listener = listener
	daemon.StartWorker(virtualMachineReconciler.Run)
	daemon.StartWorker(imageReconciler.Run)
	return daemon.Serve(server)
}
