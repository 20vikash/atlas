package firecracker

import (
	"context"
	"strconv"

	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// RefreshMetadata replaces the current MMDS data for an active process.
func (runtime *Runtime) RefreshMetadata(ctx context.Context, input vm.RuntimeMachine) error {
	status, err := runtime.Inspect(ctx, input)
	if err != nil {
		return err
	}
	if status.State != vm.StateRunning && status.State != vm.StatePaused {
		return nil
	}
	return api.New(runtime.configuration.sockPath(input.ID)).PutMMDS(ctx, metadataServiceData(
		input.ID,
		input.NetworkInterface.GuestIPAddress,
		input.NetworkInterface.MACAddress,
		input.Specification,
	))
}

func metadataServiceData(virtualMachineID, ipAddress, macAddress string, specification vm.Specification) map[string]any {
	publicKeys := make(map[string]any, len(specification.SSHKeys))
	for keyIndex, sshKey := range specification.SSHKeys {
		publicKeys[strconv.Itoa(keyIndex)] = map[string]any{"openssh-key": sshKey}
	}

	metadata := map[string]any{"instance-id": virtualMachineID, "public-keys": publicKeys}
	if specification.Hostname != "" {
		metadata["local-hostname"] = specification.Hostname
	}
	if ipAddress != "" {
		metadata["local-ipv4"] = ipAddress
	}
	if macAddress != "" {
		metadata["mac"] = macAddress
	}
	if specification.Network.PublicIPv4 != "" {
		metadata["public-ipv4"] = specification.Network.PublicIPv4
	}
	if specification.Network.WireGuardMeshIPv6 != "" {
		metadata["mesh-ipv6"] = specification.Network.WireGuardMeshIPv6
	}
	if len(specification.Metadata) > 0 {
		customMetadata := make(map[string]any, len(specification.Metadata))
		for key, value := range specification.Metadata {
			customMetadata[key] = value
		}
		metadata["attributes"] = customMetadata
	}

	data := map[string]any{"latest": map[string]any{"meta-data": metadata}}
	if specification.UserData != "" {
		data["latest"].(map[string]any)["user-data"] = specification.UserData
	}
	return data
}
