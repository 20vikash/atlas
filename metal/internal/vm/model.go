package vm

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// Specification contains the persistent configuration for one VM.
type Specification struct {
	VirtualCPUCount int                  `json:"virtual_cpu_count"`
	MemoryMiB       int                  `json:"memory_mib"`
	DiskMiB         int                  `json:"disk_mib"`
	Disk            Disk                 `json:"disk"`
	Image           Image                `json:"image"`
	Network         NetworkConfiguration `json:"network"`
	SSHKeys         []string             `json:"ssh_keys"`
	Hostname        string               `json:"hostname"`
	UserData        string               `json:"user_data"`
	Metadata        map[string]string    `json:"metadata"`
	// IsSleepy allows automatic sleep for an idle VM.
	IsSleepy bool `json:"is_sleepy"`
}

// Image identifies immutable boot files and their transport URLs.
type Image struct {
	Name                        string                       `json:"name"`
	RootfsURL                   string                       `json:"rootfs_url"`
	RootfsSHA256                string                       `json:"rootfs_sha256"`
	KernelURL                   string                       `json:"kernel_url"`
	KernelSHA256                string                       `json:"kernel_sha256"`
	Architecture                string                       `json:"architecture"`
	CacheImage                  bool                         `json:"cache_image"`
	MemorySnapshot              bool                         `json:"memory_snapshot"`
	MemorySnapshotConfiguration *MemorySnapshotConfiguration `json:"memory_snapshot_configuration,omitempty"`
}

// MemorySnapshotConfiguration is the exact shape of a local warm image.
type MemorySnapshotConfiguration struct {
	VirtualCPUCount int `json:"virtual_cpu_count"`
	MemoryMiB       int `json:"memory_mib"`
	DiskMiB         int `json:"disk_mib"`
}

// Disk contains the requested VM disk limits. A zero value is unlimited.
type Disk struct {
	ThroughputMiBps int `json:"throughput_mibps"`
	IOPS            int `json:"iops"`
}

// NetworkConfiguration contains the requested VM network configuration.
type NetworkConfiguration struct {
	PublicIPv4                    string `json:"public_ipv4"`
	WireGuardMeshIPv6             string `json:"wireguard_mesh_ipv6"`
	PrivateNetworkThroughputMiBps int    `json:"private_network_throughput_mibps"`
	PublicNetworkThroughputMiBps  int    `json:"public_network_throughput_mibps"`
	Egress                        Egress `json:"egress"`
}

// SameReservation reports whether two specifications reserve the same VM.
func (specification Specification) SameReservation(other Specification) bool {
	return specification.VirtualCPUCount == other.VirtualCPUCount &&
		specification.MemoryMiB == other.MemoryMiB &&
		specification.DiskMiB == other.DiskMiB &&
		specification.Disk == other.Disk &&
		specification.Image.Name == other.Image.Name &&
		strings.EqualFold(specification.Image.RootfsSHA256, other.Image.RootfsSHA256) &&
		strings.EqualFold(specification.Image.KernelSHA256, other.Image.KernelSHA256) &&
		specification.Image.Architecture == other.Image.Architecture &&
		specification.Network == other.Network &&
		slices.Equal(specification.SSHKeys, other.SSHKeys) &&
		specification.Hostname == other.Hostname &&
		specification.UserData == other.UserData &&
		specification.IsSleepy == other.IsSleepy &&
		maps.Equal(specification.Metadata, other.Metadata)
}

// RefreshImageSource replaces image URLs and caching intent.
func (specification Specification) RefreshImageSource(other Specification) Specification {
	specification.Image.RootfsURL = other.Image.RootfsURL
	specification.Image.KernelURL = other.Image.KernelURL
	specification.Image.CacheImage = other.Image.CacheImage
	specification.Image.MemorySnapshot = other.Image.MemorySnapshot
	specification.Image.MemorySnapshotConfiguration = other.Image.MemorySnapshotConfiguration
	return specification
}

// Egress controls internet reachability without changing mesh access.
type Egress string

const (
	// EgressUplink routes VM traffic to the internet through the host uplink.
	EgressUplink Egress = "uplink"
	// EgressMesh keeps the private network attachment and gives no internet path.
	EgressMesh Egress = "mesh"
	// EgressNone removes the private network attachment and isolates the VM.
	EgressNone Egress = "none"
)

// IsValid reports whether the value names one egress mode.
func (egress Egress) IsValid() bool {
	switch egress {
	case EgressUplink, EgressMesh, EgressNone:
		return true
	default:
		return false
	}
}

// HasVirtualEthernet reports whether the mode keeps the private network attachment.
func (egress Egress) HasVirtualEthernet() bool {
	return egress == EgressUplink || egress == EgressMesh
}

// HasInternetPath reports whether the mode gives the VM a route to the internet.
func (egress Egress) HasInternetPath() bool {
	return egress == EgressUplink
}

// State is a virtual machine condition.
type State string

const (
	// StateUnknown means the runtime has not been inspected yet.
	StateUnknown State = "unknown"
	// StateCreated means the guest exists but has not started.
	StateCreated State = "created"
	// StateRunning means the guest is executing.
	StateRunning State = "running"
	// StatePaused means the guest is resident but not executing.
	StatePaused State = "paused"
	// StateStopped means the guest is not running and can start again.
	StateStopped State = "stopped"
	// StateFailed means the runtime stopped the guest unexpectedly.
	StateFailed State = "failed"
	// StateSleeping means the guest memory is in a snapshot and no process runs.
	StateSleeping State = "sleeping"
	// StateDestroyed means every host resource is released.
	StateDestroyed State = "destroyed"
)

// IsDesiredState reports whether state is a valid requested state.
func IsDesiredState(state State) bool {
	switch state {
	case StateRunning, StateStopped, StatePaused, StateDestroyed:
		return true
	default:
		return false
	}
}

// isObservedState reports whether state is one a runtime can report.
func isObservedState(state State) bool {
	switch state {
	case StateUnknown, StateCreated, StateRunning, StatePaused, StateStopped, StateFailed, StateSleeping, StateDestroyed:
		return true
	default:
		return false
	}
}

// Information describes a virtual machine.
type Information struct {
	ID                            string
	State                         State
	DesiredState                  State
	Error                         *PublicOperationError
	VirtualCPUCount               int
	MemoryMiB                     int
	DiskMiB                       int
	DiskUsedMiB                   int
	DiskThroughputMiBps           int
	DiskIOPS                      int
	Image                         Image
	SSHKeys                       []string
	Hostname                      string
	Metadata                      map[string]string
	IsSleepy                      bool
	MAC                           string
	PublicIPv4                    string
	WireGuardMeshIPv6             string
	PrivateNetworkThroughputMiBps int
	PublicNetworkThroughputMiBps  int
	Egress                        Egress
	DesiredGeneration             uint64
	DesiredRestartGeneration      uint64
	ObservedGeneration            uint64
	ObservedRestartGeneration     uint64
	Phase                         string
	OperationID                   string
	OperationStartedAt            time.Time
	UpdatedAt                     time.Time
	LastNetworkActivityAt         time.Time
	SleepingSince                 time.Time
}

// PublicOperationError contains safe reconciliation error data.
type PublicOperationError struct {
	Code      string
	Message   string
	UpdatedAt time.Time
}
