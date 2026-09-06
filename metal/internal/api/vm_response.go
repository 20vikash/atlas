package api

import (
	"maps"

	"github.com/frappe/atlas/metal/internal/vm"
)

type virtualMachineResponse struct {
	ID       string                         `json:"id"`
	Desired  desiredVirtualMachineResponse  `json:"desired"`
	Observed observedVirtualMachineResponse `json:"observed"`
}

type desiredVirtualMachineResponse struct {
	Generation        uint64                      `json:"generation"`
	RestartGeneration uint64                      `json:"restart_generation"`
	State             string                      `json:"state"`
	Compute           computeResponse             `json:"compute"`
	Disk              diskResponse                `json:"disk"`
	Image             virtualMachineImageResponse `json:"image"`
	Network           networkResponse             `json:"network"`
	Guest             guestResponse               `json:"guest"`
}

type observedVirtualMachineResponse struct {
	Generation        uint64                  `json:"generation"`
	RestartGeneration uint64                  `json:"restart_generation"`
	State             string                  `json:"state"`
	Phase             string                  `json:"phase,omitempty"`
	OperationID       string                  `json:"operation_id,omitempty"`
	OperationStarted  string                  `json:"operation_started_at,omitempty"`
	UpdatedAt         string                  `json:"updated_at"`
	Disk              observedDiskResponse    `json:"disk"`
	Network           observedNetworkResponse `json:"network"`
	Error             *operationErrorResponse `json:"error"`
}

type computeResponse struct {
	VirtualCPUCount int `json:"virtual_cpu_count"`
	MemoryMiB       int `json:"memory_mib"`
}

type guestResponse struct {
	Hostname string            `json:"hostname"`
	SSHKeys  []string          `json:"ssh_keys"`
	Metadata map[string]string `json:"metadata"`
}

type virtualMachineImageResponse struct {
	Ref                         string                               `json:"ref"`
	Architecture                string                               `json:"architecture"`
	Rootfs                      imageArtifactResponse                `json:"rootfs"`
	Kernel                      imageArtifactResponse                `json:"kernel"`
	CacheImage                  bool                                 `json:"cache_image"`
	MemorySnapshot              bool                                 `json:"memory_snapshot"`
	MemorySnapshotConfiguration *memorySnapshotConfigurationResponse `json:"memory_snapshot_configuration,omitempty"`
}

type memorySnapshotConfigurationResponse struct {
	VirtualCPUCount int `json:"virtual_cpu_count"`
	MemoryMiB       int `json:"memory_mib"`
	DiskMiB         int `json:"disk_mib"`
}

type imageArtifactResponse struct {
	SHA256 string `json:"sha256"`
}

type networkResponse struct {
	PublicIPv4                    string `json:"public_ipv4,omitempty"`
	WireGuardMeshIPv6             string `json:"wireguard_mesh_ipv6"`
	PrivateNetworkThroughputMiBps int    `json:"private_network_throughput_mibps"`
	PublicNetworkThroughputMiBps  int    `json:"public_network_throughput_mibps"`
	Egress                        string `json:"egress"`
}

type diskResponse struct {
	ThroughputMiBps int `json:"throughput_mibps"`
	IOPS            int `json:"iops"`
	SizeMiB         int `json:"size_mib"`
}

type observedDiskResponse struct {
	UsedMiB int `json:"used_mib"`
}

type observedNetworkResponse struct {
	MAC string `json:"mac,omitempty"`
}

type operationErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	UpdatedAt string `json:"updated_at"`
}

func toVirtualMachine(information vm.Info) virtualMachineResponse {
	return virtualMachineResponse{
		ID: information.ID,
		Desired: desiredVirtualMachineResponse{
			Generation:        information.DesiredGeneration,
			RestartGeneration: information.DesiredRestartGeneration,
			State:             string(information.DesiredState),
			Compute: computeResponse{
				VirtualCPUCount: information.VCPUs,
				MemoryMiB:       information.MemoryMiB,
			},
			Disk: diskResponse{
				ThroughputMiBps: information.DiskThroughputMiBps,
				IOPS:            information.DiskIOPS,
				SizeMiB:         information.DiskMiB,
			},
			Image: toVirtualMachineImage(information.Image),
			Network: networkResponse{
				PublicIPv4:                    information.PublicIPv4,
				WireGuardMeshIPv6:             information.WireGuardMeshIPv6,
				PrivateNetworkThroughputMiBps: information.PrivateNetworkThroughputMiBps,
				PublicNetworkThroughputMiBps:  information.PublicNetworkThroughputMiBps,
				Egress:                        string(information.Egress),
			},
			Guest: guestResponse{
				Hostname: information.Hostname,
				SSHKeys:  append([]string{}, information.SSHKeys...),
				Metadata: cloneMetadata(information.Metadata),
			},
		},
		Observed: observedVirtualMachineResponse{
			Generation:        information.ObservedGeneration,
			RestartGeneration: information.ObservedRestartGeneration,
			State:             string(information.State),
			Phase:             information.Phase,
			OperationID:       information.OperationID,
			OperationStarted:  formatRFC3339(information.OperationStartedAt),
			UpdatedAt:         formatRFC3339(information.UpdatedAt),
			Disk:              observedDiskResponse{UsedMiB: information.DiskUsedMiB},
			Network:           observedNetworkResponse{MAC: information.MAC},
			Error:             toOperationError(information.Error),
		},
	}
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return map[string]string{}
	}
	return maps.Clone(metadata)
}

func toOperationError(operationError *vm.PublicOperationError) *operationErrorResponse {
	if operationError == nil {
		return nil
	}
	return &operationErrorResponse{
		Code:      operationError.Code,
		Message:   operationError.Message,
		UpdatedAt: formatRFC3339(operationError.UpdatedAt),
	}
}

func toVirtualMachineImage(image vm.ImageRef) virtualMachineImageResponse {
	return virtualMachineImageResponse{
		Ref:                         image.Name,
		Architecture:                image.Architecture,
		Rootfs:                      imageArtifactResponse{SHA256: image.RootfsSHA256},
		Kernel:                      imageArtifactResponse{SHA256: image.KernelSHA256},
		CacheImage:                  image.CacheImage,
		MemorySnapshot:              image.MemorySnapshot,
		MemorySnapshotConfiguration: toMemorySnapshotConfiguration(image.MemorySnapshotConfiguration),
	}
}

func toMemorySnapshotConfiguration(configuration *vm.MemorySnapshotConfiguration) *memorySnapshotConfigurationResponse {
	if configuration == nil {
		return nil
	}

	return &memorySnapshotConfigurationResponse{
		VirtualCPUCount: configuration.VirtualCPUCount,
		MemoryMiB:       configuration.MemoryMiB,
		DiskMiB:         configuration.DiskMiB,
	}
}
