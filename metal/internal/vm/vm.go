package vm

import "time"

// Info describes a virtual machine.
type Info struct {
	ID                            string
	State                         State
	DesiredState                  State
	Error                         string
	VCPUs                         int
	MemoryMiB                     int
	DiskMiB                       int
	DiskUsedMiB                   int
	DiskThroughputMiBps           int
	DiskIOPS                      int
	Image                         ImageRef
	SSHKeys                       []string
	Hostname                      string
	Metadata                      map[string]string
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
}
