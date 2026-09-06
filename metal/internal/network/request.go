// Package network manages host virtual machine networks.
package network

import "github.com/frappe/atlas/metal/internal/vm"

// ReleaseRequest identifies the virtual machine network to remove.
type ReleaseRequest = vm.NetworkReleaseRequest

type request struct {
	VirtualMachineID              string
	Egress                        vm.Egress
	PublicIPv4                    string
	WireGuardMeshIPv6             string
	PrivateNetworkThroughputMiBps int
	PublicNetworkThroughputMiBps  int
	UserID                        uint32
	GroupID                       uint32
}

func (request request) trafficControl() trafficControlRequest {
	return trafficControlRequest{
		VirtualMachineID:              request.VirtualMachineID,
		UserID:                        request.UserID,
		Egress:                        request.Egress,
		PrivateNetworkThroughputMiBps: request.PrivateNetworkThroughputMiBps,
		PublicNetworkThroughputMiBps:  request.PublicNetworkThroughputMiBps,
	}
}

// Interface contains one virtual machine network interface.
type Interface = vm.NetworkInterface
