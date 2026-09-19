package main

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/cilium/ebpf"
	"github.com/spf13/cobra"
)

type debugStats struct {
	Accepted         uint64
	Dropped          uint64
	ProtocolSent     uint64
	ProtocolReceived uint64
	Lost             uint64
}

type debugEvent struct {
	Timestamp   uint64
	Source      [16]byte
	Destination [16]byte
	VM          [16]byte
	Host        [16]byte
	Tenant      [4]byte
	Hook        uint8
	Verdict     uint8
	Operation   uint8
	Direction   uint8
}

type debugFilter struct {
	source      netip.Addr
	destination netip.Addr
	tenant      *uint32
	action      *uint8
}

var (
	inspectAddressText                  string
	dumpSourceText, dumpDestinationText string
	dumpActionText                      string
	dumpTenant                          uint32
	topSourceText, topDestinationText   string
	topTenant                           uint32
)

var debugCommand = &cobra.Command{
	Use:   "debug",
	Short: "inspect BPF decisions",
	Args:  cobra.NoArgs,
	RunE:  showHelp,
}

var debugEnableCommand = &cobra.Command{
	Use:   "enable",
	Short: "enable debug events",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return setDebug(true)
	},
}

var debugDisableCommand = &cobra.Command{
	Use:   "disable",
	Short: "disable debug events",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return setDebug(false)
	},
}

var debugStatusCommand = &cobra.Command{
	Use:   "status",
	Short: "show debug counters",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return showDebugStatus()
	},
}

var inspectCommand = &cobra.Command{
	Use:   "inspect",
	Short: "show the route for a VM address",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return inspectVirtualMachine(inspectAddressText)
	},
}

var dumpCommand = &cobra.Command{
	Use:   "dump",
	Short: "stream packet and protocol events",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		filter, err := newDebugFilter(dumpSourceText, dumpDestinationText, dumpActionText, dumpTenant, command.Flags().Changed("tenant"))
		if err != nil {
			return err
		}

		return dumpDebug(filter)
	},
}

var topCommand = &cobra.Command{
	Use:   "top",
	Short: "show live packet decisions by VM pair",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, _ []string) error {
		filter, err := newDebugFilter(topSourceText, topDestinationText, "", topTenant, command.Flags().Changed("tenant"))
		if err != nil {
			return err
		}

		return topDebug(filter)
	},
}

func setDebug(enabled bool) error {
	config, err := openMap("debug_config")
	if err != nil {
		return err
	}
	defer config.Close()

	value := uint8(0)

	if enabled {
		value = 1

		if err := resetDebugStats(); err != nil {
			return err
		}
	}

	return config.Put(uint32(0), value)
}

func resetDebugStats() error {
	statsMap, err := openMap("debug_stats")
	if err != nil {
		return err
	}
	defer statsMap.Close()

	return statsMap.Put(uint32(0), make([]debugStats, ebpf.MustPossibleCPU()))
}

func showDebugStatus() error {
	config, err := openMap("debug_config")
	if err != nil {
		return err
	}
	defer config.Close()

	var enabled uint8

	if err := config.Lookup(uint32(0), &enabled); err != nil {
		return err
	}

	stats, err := readDebugStats()
	if err != nil {
		return err
	}

	status := "disabled"
	if enabled != 0 {
		status = "enabled"
	}

	fmt.Printf("debug: %s\n"+"accepted: %d\n"+"dropped: %d\n"+"protocol sent: %d\n"+"protocol received: %d\n"+"events lost: %d\n", status, stats.Accepted, stats.Dropped, stats.ProtocolSent, stats.ProtocolReceived, stats.Lost)

	return nil
}

func newDebugFilter(sourceText, destinationText, actionText string, tenant uint32, hasTenant bool) (debugFilter, error) {
	filter := debugFilter{}

	var err error

	if sourceText != "" {
		filter.source, err = netip.ParseAddr(sourceText)
		if err != nil {
			return filter, err
		}
	}

	if destinationText != "" {
		filter.destination, err = netip.ParseAddr(destinationText)
		if err != nil {
			return filter, err
		}
	}

	if hasTenant {
		filter.tenant = &tenant
	}

	if actionText != "" {
		action, err := parseAction(actionText)
		if err != nil {
			return filter, err
		}

		filter.action = &action
	}

	return filter, nil
}

func readDebugStats() (debugStats, error) {
	statsMap, err := openMap("debug_stats")
	if err != nil {
		return debugStats{}, err
	}
	defer statsMap.Close()

	perCPU := make([]debugStats, ebpf.MustPossibleCPU())

	if err := statsMap.Lookup(uint32(0), &perCPU); err != nil {
		return debugStats{}, err
	}

	var total debugStats

	for _, stats := range perCPU {
		total.Accepted += stats.Accepted
		total.Dropped += stats.Dropped
		total.ProtocolSent += stats.ProtocolSent
		total.ProtocolReceived += stats.ProtocolReceived
		total.Lost += stats.Lost
	}

	return total, nil
}

const debugVMHook uint8 = 0

func (filter debugFilter) matches(event debugEvent) bool {
	isVMEvent := event.Hook == debugVMHook

	if filter.tenant != nil &&
		binary.BigEndian.Uint32(event.Tenant[:]) != *filter.tenant {
		return false
	}

	if filter.action != nil &&
		(!isVMEvent || event.Verdict != *filter.action) {
		return false
	}

	if filter.source.IsValid() &&
		isVMEvent &&
		netip.AddrFrom16(event.Source) != filter.source {
		return false
	}

	if filter.destination.IsValid() &&
		isVMEvent &&
		netip.AddrFrom16(event.Destination) != filter.destination {
		return false
	}

	return true
}

func parseAction(action string) (uint8, error) {
	switch action {
	case "accept":
		return 0, nil
	case "drop":
		return 1, nil
	case "redirect":
		return 2, nil
	default:
		return 0, fmt.Errorf("unknown action %q; use accept, drop, or redirect", action)
	}
}

func hookName(hook uint8) string {
	return enumName(
		hook,
		[]string{
			"VM",
			"NDP",
			"WIREGUARD",
			"UNICAST",
		},
	)
}

func verdictName(verdict uint8) string {
	return enumName(
		verdict,
		[]string{
			"ACCEPT",
			"DROP",
			"REDIRECT",
		},
	)
}

func directionName(direction uint8) string {
	return enumName(
		direction,
		[]string{
			"",
			"TX",
			"RX",
		},
	)
}

func vmOperationName(operation uint8) string {
	return enumName(
		operation,
		[]string{
			"",
			"UNDERLAY",
			"NOT_VIRTUAL",
			"SOURCE_NOT_OWNED",
			"TENANT_DENIED",
			"LOCAL_DESTINATION",
			"REMOTE_UNKNOWN",
			"NO_CONFIG",
			"ENCAP_FAILED",
			"ENCAP_SUCCEEDED",
		},
	)
}

const debugUnicastHook uint8 = 3

func unicastOperationName(operation uint8) string {
	return enumName(operation, []string{
		"",
		"UNICAST_TX_OWNER",
		"UNICAST_TX_FAN_OUT",
		"UNICAST_TX_ADVERTISEMENT",
		"UNICAST_TX_FAILED",
		"UNICAST_RX_REJECTED",
		"UNICAST_RX_REQUESTER",
		"UNICAST_RX_LEARNED",
		"UNICAST_RX_UNKNOWN_PEER",
		"UNICAST_RX_LEARN_FAILED",
	})
}

const debugWireguardHook uint8 = 2

func wireguardOperationName(operation uint8) string {
	return enumName(operation, []string{
		"",
		"WIREGUARD_NOT_HERE_SENT",
		"WIREGUARD_NOT_HERE_RECEIVED",
	})
}

func operationName(operation uint8) string {
	return enumName(operation, []string{
		"",
		"NDP_LEARN",
		"NDP_UNKNOWN_PEER",
		"NDP_LEARN_FAILED",
	})
}

func debugOperationName(event debugEvent) string {
	if event.Operation == 0 {
		return ""
	}

	if event.Hook == debugVMHook {
		return vmOperationName(event.Operation)
	}

	if event.Hook == debugUnicastHook {
		return unicastOperationName(event.Operation)
	}

	if event.Hook == debugWireguardHook {
		return wireguardOperationName(event.Operation)
	}

	return operationName(event.Operation)
}

func enumName(value uint8, names []string) string {
	if int(value) < len(names) {
		return names[value]
	}

	return fmt.Sprintf("UNKNOWN(%d)", value)
}
