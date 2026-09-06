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
	"github.com/frappe/atlas/metal/internal/reconciler"
	"github.com/frappe/atlas/metal/internal/storage"
	"github.com/frappe/atlas/metal/internal/systemd"
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
	// Atlas WG Mesh is required, so fail before metald builds anything.
	mesh, err := connectMesh(o)
	if err != nil {
		return err
	}
	if err := makeDirs(o); err != nil {
		return err
	}
	units, err := systemd.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("connect systemd: %w", err)
	}

	daemonContext, cancelDaemon := context.WithCancel(context.Background())
	stores := storage.NewStores(daemonContext, o.pool, o.imagesDir, logger)
	consoleBroker := console.NewBroker(filepath.Join(o.cfg.SocketsDir, "consoles"))
	daemon := newDaemon(daemonContext, cancelDaemon, logger, stores.Snapshots, consoleBroker, units)
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
	networkManager := network.NewLinuxAllocator(mesh)
	virtualMachineRuntime := firecracker.NewRuntime(
		o.cfg,
		units,
		stores.VirtualMachines,
		stores.Images,
		consoleBroker,
		logger,
	)
	virtualMachineManager, err := vm.NewManager(
		vm.ManagerConfig{MachinesDirectory: o.cfg.MachinesDir},
		vm.ManagerDependencies{
			Runtime:   virtualMachineRuntime,
			Network:   networkManager,
			Storage:   stores.VirtualMachines,
			Snapshots: stores.Snapshots,
			Logger:    logger,
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

	virtualMachineReconciler := reconciler.New(
		virtualMachineManager,
		reconcileInterval,
		reconciler.Config{Logger: logger},
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
		ConsoleBroker:         consoleBroker,
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
