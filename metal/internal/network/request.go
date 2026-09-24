// Package network manages host virtual machine networks.
package network

import "github.com/frappe/atlas/metal/internal/vm"

// ReleaseRequest identifies the virtual machine network to remove.
type ReleaseRequest = vm.NetworkReleaseRequest

type request struct {
	vm.NetworkConfiguration
	VirtualMachineID string
	UserID           uint32
	GroupID          uint32
}

// trafficControl narrows the request to the fields the policers need.
func (request request) trafficControl() trafficControlRequest {
	return trafficControlRequest{
		VirtualMachineID:              request.VirtualMachineID,
		UserID:                        request.UserID,
		HasNetworkAttachment:          request.HasNetworkAttachment(),
		HasIPv4HostRoute:              request.HasIPv4HostRoute(),
		PrivateNetworkThroughputMiBps: request.PrivateNetworkThroughputMiBps,
		PublicNetworkThroughputMiBps:  request.PublicNetworkThroughputMiBps,
	}
}

// Interface contains one virtual machine network interface.
type Interface = vm.NetworkInterface
