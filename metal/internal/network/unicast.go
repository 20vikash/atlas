package network

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
)

// UnicastConfig identifies the daemon binary and the peer state of one host.
type UnicastConfig struct {
	// BinaryPath runs the unicast NDP transport daemon.
	BinaryPath string
	// WireGuardStatePath holds the managed peers that the daemon reads.
	WireGuardStatePath string
}

// UnicastManager runs the unicast NDP transport daemon as a supervised child
// process. metald owns the lifecycle: Enable starts the daemon, Disable stops
// it cleanly, and the next synchronization restarts a crashed daemon.
type UnicastManager struct {
	configuration UnicastConfig
	spawner       unicastSpawner
	mutex         sync.Mutex
	process       unicastProcess
}

// unicastSpawner starts one daemon process. Tests replace it.
type unicastSpawner interface {
	Start(configuration UnicastConfig) (unicastProcess, error)
}

// unicastProcess is one running daemon process.
type unicastProcess interface {
	// Exited reports whether the process already ended.
	Exited() bool
	// Terminate stops the process cleanly and waits for its exit.
	Terminate(context.Context) error
}

// NewUnicastManager creates a daemon supervisor for one host.
func NewUnicastManager(configuration UnicastConfig) (*UnicastManager, error) {
	if configuration.BinaryPath == "" {
		return nil, fmt.Errorf("unicast daemon binary path is required")
	}
	if configuration.WireGuardStatePath == "" {
		return nil, fmt.Errorf("WireGuard peer state path is required")
	}

	return &UnicastManager{configuration: configuration, spawner: hostUnicastSpawner{}}, nil
}

// Enable starts the daemon when the managed peer set holds a peer. A host
// without a peer stays in multicast mode, because unicast transport has no
// destination. A running daemon is left alone, and a crashed daemon starts
// again.
func (manager *UnicastManager) Enable(ctx context.Context) error {
	peers, err := loadWireGuardPeers(manager.configuration.WireGuardStatePath)
	if err != nil {
		return fmt.Errorf("load WireGuard peers: %w", err)
	}

	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if len(peers) == 0 {
		return manager.stop(ctx)
	}
	if manager.process != nil && !manager.process.Exited() {
		return nil
	}

	process, spawnErr := manager.spawner.Start(manager.configuration)
	if spawnErr != nil {
		return fmt.Errorf("start unicast daemon: %w", spawnErr)
	}
	manager.process = process
	return nil
}

// Disable stops the daemon, which restores the multicast NDP filters.
func (manager *UnicastManager) Disable(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	return manager.stop(ctx)
}

// stop terminates the running daemon, if any.
func (manager *UnicastManager) stop(ctx context.Context) error {
	if manager.process == nil {
		return nil
	}

	if err := manager.process.Terminate(ctx); err != nil {
		return fmt.Errorf("stop unicast daemon: %w", err)
	}
	manager.process = nil
	return nil
}

// hostUnicastSpawner starts the daemon on this host. Tests replace it.
type hostUnicastSpawner struct{}

// Start launches the daemon with a parent-death signal, so a metald crash also stops the daemon cleanly.
func (hostUnicastSpawner) Start(configuration UnicastConfig) (unicastProcess, error) {
	command := exec.Command(configuration.BinaryPath, "unicast", "start", configuration.WireGuardStatePath)
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}

	if err := command.Start(); err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()

	return &hostUnicastProcess{command: command, done: done}, nil
}

// hostUnicastProcess is one daemon process on this host. The waiter goroutine
// ends when the process exits, so the process owns no other goroutine.
type hostUnicastProcess struct {
	command *exec.Cmd
	done    chan error
}

// Exited reports whether the process already ended.
func (process *hostUnicastProcess) Exited() bool {
	select {
	case <-process.done:
		return true
	default:
		return false
	}
}

// Terminate signals the daemon to stop and waits for its clean exit. A daemon that ignores the signal is killed when the context ends.
func (process *hostUnicastProcess) Terminate(ctx context.Context) error {
	if process.Exited() {
		return nil
	}

	if err := process.command.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		_ = process.command.Process.Kill()
		<-process.done
		return fmt.Errorf("unicast daemon ignored the stop signal: %w", ctx.Err())
	}
}
