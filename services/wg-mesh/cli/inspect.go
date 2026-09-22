package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/cilium/ebpf"
	"github.com/spf13/cobra"
)

var inspectJSON bool

var inspectCommand = &cobra.Command{
	Use:   "inspect ADDRESS",
	Short: "show the mesh state of one VM address",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, arguments []string) error {
		return inspectVM(arguments[0])
	},
}

func init() {
	inspectCommand.Flags().BoolVar(&inspectJSON, "json", false, "print JSON")
	rootCommand.AddCommand(inspectCommand)
}

type inspectedRoute struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}

type inspectedVM struct {
	Address    string           `json:"address"`
	Location   string           `json:"location"`
	Interface  string           `json:"interface,omitempty"`
	RemoteHost string           `json:"remote_host,omitempty"`
	Route      string           `json:"route,omitempty"`
	Privileged bool             `json:"privileged"`
	Gateway    bool             `json:"gateway"`
	Prefixes   []string         `json:"prefixes,omitempty"`
	Routes     []inspectedRoute `json:"routes,omitempty"`
}

func inspectVM(addressText string) error {
	address, err := parseMeshAddress(addressText)
	if err != nil {
		return err
	}

	state := inspectedVM{Address: netip.AddrFrom16(address).String(), Location: "unknown"}
	if _, err := readMap[uint8]("privileged_vms", address); err == nil {
		state.Privileged = true
	} else if !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	ifindex, local, err := localVM(address)
	if err != nil {
		return err
	}
	if local {
		state.Location = "local"
		state.Interface = deviceName(ifindex)
		if err := fillLocalVMState(&state, address, ifindex); err != nil {
			return err
		}
	} else if err := fillRemoteVMState(&state, address); err != nil {
		return err
	}

	if inspectJSON {
		return json.NewEncoder(os.Stdout).Encode(state)
	}
	printInspectedVM(state)
	return nil
}

func localVM(address [16]byte) (uint32, bool, error) {
	ifindex, err := readMap[uint32]("local_vms", address)
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return 0, false, nil
	}
	return ifindex, err == nil, err
}

func fillLocalVMState(state *inspectedVM, address [16]byte, ifindex uint32) error {
	localGateway, err := readMap[gateway]("local_gateway", uint32(0))
	if err != nil {
		return err
	}
	state.Gateway = localGateway.IfIndex == ifindex && localGateway.Address == address

	owners, err := readMapEntries[prefixKey, uint32]("owned_prefixes")
	if err != nil {
		return err
	}
	for prefix, owner := range owners {
		if owner == ifindex {
			state.Prefixes = append(state.Prefixes, prefixText(prefix))
		}
	}
	sort.Strings(state.Prefixes)

	routes, err := readMapEntries[routeKey, [16]byte]("gateway_routes")
	if err != nil {
		return err
	}
	for key, gatewayAddress := range routes {
		if key.VirtualMachine == address {
			state.Routes = append(state.Routes, inspectedRoute{
				Destination: prefixText(prefixKey{key.PrefixLength - 128, key.Destination}),
				Gateway:     netip.AddrFrom16(gatewayAddress).String(),
			})
		}
	}
	sort.Slice(state.Routes, func(left, right int) bool {
		return state.Routes[left].Destination < state.Routes[right].Destination
	})
	return nil
}

func fillRemoteVMState(state *inspectedVM, address [16]byte) error {
	host, err := readMap[[16]byte]("remote_vms", address)
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	state.Location = "remote"
	state.RemoteHost = netip.AddrFrom16(host).String()
	route, err := commandOutput("ip", "-6", "route", "get", state.RemoteHost)
	if err != nil {
		return err
	}
	state.Route = strings.TrimSpace(route)
	return nil
}

func printInspectedVM(state inspectedVM) {
	fmt.Printf("address\t%s\nlocation\t%s\n", state.Address, state.Location)
	if state.Interface != "" {
		fmt.Printf("interface\t%s\nprivileged\t%t\ngateway\t%t\n", state.Interface, state.Privileged, state.Gateway)
	}
	if state.RemoteHost != "" {
		fmt.Printf("remote host\t%s\nroute\t%s\n", state.RemoteHost, state.Route)
	}
	for _, prefix := range state.Prefixes {
		fmt.Printf("prefix\t%s\n", prefix)
	}
	for _, route := range state.Routes {
		fmt.Printf("route\t%s\t%s\n", route.Destination, route.Gateway)
	}
}
