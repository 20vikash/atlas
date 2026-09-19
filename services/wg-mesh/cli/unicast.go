package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
)

// Must match ATLAS_UNICAST_PEER_LIMIT in bpf/state.h and the peer_list map.
const unicastPeerLimit = 256

const unicastLockPath = "/run/lock/atlas-wg-mesh-unicast.lock"

// Filter priorities on the uplink. A start or stop passes through a short
// window with both hook sets attached: ingress unicast decaps before the
// multicast hook at priority 10, and egress unicast wraps after it.
const (
	unicastIngressFilterPriority = "5"
	unicastEgressFilterPriority  = "15"
)

var unicastCommand = &cobra.Command{
	Use:   "unicast",
	Short: "transport NDP over a routed IPv4 underlay",
	Args:  cobra.NoArgs,
	RunE:  showHelp,
}

var unicastStartCommand = &cobra.Command{
	Use:   "start PEERS_JSON",
	Short: "attach the unicast hooks and serve the unicast NDP transport",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, arguments []string) error {
		return runUnicastDaemon(arguments[0])
	},
}

// runUnicastDaemon switches the discovery interface to unicast NDP transport
// and waits for a stop signal. The BPF hooks do the packet processing, and the
// peer maps come from the WireGuard peer state through peers sync.
//
// On start, the daemon attaches the unicast hooks and removes the multicast
// NDP filters from the uplink. On a clean stop, it restores the multicast
// filters first, so neighbour discovery never stops. A failed start leaves
// the multicast filters in place.
func runUnicastDaemon(peersPath string) error {
	config, err := readPinnedConfig()
	if err != nil {
		return err
	}
	uplinkName := interfaceWithIPv4(config.UplinkIPv4)
	if uplinkName == "" {
		return errors.New("cannot find the configured uplink")
	}
	peers, err := readMeshPeers(peersPath, config)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		return fmt.Errorf("%s holds no usable peers", peersPath)
	}

	unlock, err := lockUnicastDaemon()
	if err != nil {
		return err
	}
	defer unlock()

	if err := attachUnicastHook(uplinkName, ndpUnicastIngressProgram, "ingress", unicastIngressFilterPriority); err != nil {
		return err
	}
	defer detachUnicastHookWarning(uplinkName, "ingress")

	if err := attachUnicastHook(uplinkName, ndpUnicastEgressProgram, "egress", unicastEgressFilterPriority); err != nil {
		return err
	}
	defer detachUnicastHookWarning(uplinkName, "egress")

	// The unicast hooks own the uplink now. A failure here leaves both hook
	// sets attached, which is safe: the unicast hooks skip work that the
	// multicast hook already did.
	if err := detachHook(uplinkName); err != nil {
		return err
	}

	fmt.Printf("Atlas WG Mesh unicast transport started on %s with %d peers\n", uplinkName, len(peers))

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)
	<-interrupted

	// Restore the multicast filters first. Both hook sets then run together
	// until the deferred unicast detachments finish, which is safe for the
	// same reason as above.
	if err := attachMulticastNeighbourHook(uplinkName); err != nil {
		fmt.Fprintf(os.Stderr, "atlas-wg-mesh: warning: restore multicast NDP: %v\n", err)
	}
	return nil
}

// attachMulticastNeighbourHook restores the multicast NDP filter on the
// uplink, returning the host to multicast behaviour.
func attachMulticastNeighbourHook(uplinkName string) error {
	return attachHook(uplinkName, ndpProgram, "ingress")
}

// attachUnicastHook attaches one pinned unicast program at its own filter
// priority, separate from the multicast hook at priority 10.
func attachUnicastHook(interfaceName, program, direction, priority string) error {
	path, err := programPath(program)
	if err != nil {
		return err
	}
	_ = runCommand("tc", "qdisc", "add", "dev", interfaceName, "clsact")
	return runCommand("tc", "filter", "replace", "dev", interfaceName, direction, "prio", priority, "handle", "1", "bpf", "direct-action", "object-pinned", path)
}

// detachUnicastHook removes one unicast filter. A missing filter is not an
// error, so a crashed daemon can be cleaned up safely.
func detachUnicastHook(interfaceName, direction, priority string) error {
	err := runCommand("tc", "filter", "del", "dev", interfaceName, direction, "prio", priority, "handle", "1", "bpf")
	if err != nil && !deleteMissing(err) {
		return err
	}
	return nil
}

func detachUnicastHookWarning(interfaceName, direction string) {
	priority := unicastEgressFilterPriority
	if direction == "ingress" {
		priority = unicastIngressFilterPriority
	}
	if err := detachUnicastHook(interfaceName, direction, priority); err != nil {
		fmt.Fprintf(os.Stderr, "atlas-wg-mesh: warning: detach unicast %s hook: %v\n", direction, err)
	}
}

// lockUnicastDaemon allows one daemon and releases automatically on a crash.
func lockUnicastDaemon() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(unicastLockPath), 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(unicastLockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("unicast daemon is already running: %w", err)
	}
	// Make the deferred unlock safe after an early release.
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}, nil
}
