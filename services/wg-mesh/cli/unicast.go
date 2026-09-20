package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
)

// Must match ATLAS_UNICAST_PEER_LIMIT in bpf/state.h and the peer_list map.
const unicastPeerLimit = 256

const unicastLockPath = "/run/lock/atlas-wg-mesh-unicast.lock"

// Filter priorities on the uplink; ingress unicast runs before, egress after, the multicast hook.
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

// runUnicastDaemon attaches the unicast hooks and serves NDP until a stop signal.
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

	keepUnicast := false
	defer func() {
		if keepUnicast {
			return
		}
		detachUnicastHookWarning(uplinkName, "egress")
		detachUnicastHookWarning(uplinkName, "ingress")
	}()

	if err := attachUnicastHook(uplinkName, ndpUnicastIngressProgram, "ingress", unicastIngressFilterPriority); err != nil {
		return err
	}

	if err := attachUnicastHook(uplinkName, ndpUnicastEgressProgram, "egress", unicastEgressFilterPriority); err != nil {
		return err
	}

	if err := detachHook(uplinkName); err != nil {
		return err
	}

	fmt.Printf("Atlas WG Mesh unicast transport started on %s with %d peers\n", uplinkName, len(peers))

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)
	<-interrupted

	if err := attachMulticastNeighbourHook(uplinkName); err != nil {
		keepUnicast = true
		return fmt.Errorf("restore multicast NDP: %w", err)
	}
	return nil
}

// attachMulticastNeighbourHook restores the multicast NDP filter on the uplink.
func attachMulticastNeighbourHook(uplinkName string) error {
	return attachHook(uplinkName, ndpProgram, "ingress")
}

// attachUnicastHook attaches one pinned unicast program at its filter priority.
func attachUnicastHook(interfaceName, program, direction, priority string) error {
	path, err := programPath(program)
	if err != nil {
		return err
	}

	return attachUnicastHookPath(interfaceName, path, direction, priority)
}

// attachUnicastHookPath attaches one pinned unicast program path at its filter priority.
func attachUnicastHookPath(interfaceName, path, direction, priority string) error {
	_ = runCommand("tc", "qdisc", "add", "dev", interfaceName, "clsact")
	return runCommand("tc", "filter", "replace", "dev", interfaceName, direction, "prio", priority, "handle", "1", "bpf", "direct-action", "object-pinned", path)
}

// unicastTransportActive reports whether the uplink holds a unicast NDP filter.
func unicastTransportActive(uplinkName string) bool {
	return unicastFilterAttached(uplinkName, "ingress", unicastIngressFilterPriority) ||
		unicastFilterAttached(uplinkName, "egress", unicastEgressFilterPriority)
}

// unicastFilterAttached reports whether one direction holds a unicast-priority TC filter.
func unicastFilterAttached(interfaceName, direction, priority string) bool {
	output, err := commandOutput("tc", "filter", "show", "dev", interfaceName, direction)
	if err != nil {
		return false
	}

	return filterPriorityPresent(output, priority)
}

// filterPriorityPresent reports whether tc filter output lists a filter at a priority.
func filterPriorityPresent(output, priority string) bool {
	fields := strings.Fields(output)

	for index := 0; index+1 < len(fields); index++ {
		if (fields[index] == "pref" || fields[index] == "prio") && fields[index+1] == priority {
			return true
		}
	}

	return false
}

// detachUnicastHook removes one unicast filter; a missing filter is not an error.
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

// lockUnicastDaemon allows one daemon; the lock releases on crash.
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
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
		})
	}, nil
}
