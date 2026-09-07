package vm

import (
	"context"
	"time"
)

// StartMode selects how Runtime.Start brings up a virtual machine.
type StartMode int

const (
	// StartNormal keeps the current warm-image and cold-boot behavior.
	StartNormal StartMode = iota
	// StartFromSleepSnapshot restores the VM-local sleep snapshot and resumes it.
	StartFromSleepSnapshot
	// StartFromSleepSnapshotPaused restores the snapshot without running the vCPUs.
	StartFromSleepSnapshotPaused
)

// StopMode selects how Runtime.Stop ends a virtual machine.
type StopMode int

const (
	// StopShutdown ends the guest with the current Ctrl+Alt+Del and bounded kill.
	StopShutdown StopMode = iota
	// StopWithSleepSnapshot pauses the guest, saves a full snapshot, and
	// terminates the Firecracker process without a guest shutdown.
	StopWithSleepSnapshot
)

// StopOutcome reports what a stop produced. A warm stop fills the published
// snapshot generation and its creation time. A shutdown leaves both zero.
type StopOutcome struct {
	SnapshotGeneration uint64
	SnapshotCreatedAt  time.Time
}

// Runtime controls virtual machine processes and guest-facing operations.
type Runtime interface {
	Inspect(context.Context, RuntimeMachine) (RuntimeStatus, error)
	Start(context.Context, RuntimeMachine, StartMode) error
	Stop(context.Context, RuntimeMachine, StopMode) (StopOutcome, error)
	Pause(context.Context, RuntimeMachine) error
	Resume(context.Context, RuntimeMachine) error
	Remove(context.Context, RuntimeMachine) error
	RefreshMetadata(context.Context, RuntimeMachine) error
	RefreshDisk(context.Context, RuntimeMachine) error
	ConnectSSH(context.Context, RuntimeMachine) (SSHConnection, error)
}

// RuntimeMachine contains the complete input for one runtime operation.
type RuntimeMachine struct {
	ID               string
	UserID           uint32
	GroupID          uint32
	Specification    Specification
	NetworkInterface NetworkInterface
	// SpecificationGeneration and RestartGeneration identify the desired state a
	// snapshot belongs to. A restore validates them against the current desired
	// record before it loads the snapshot.
	SpecificationGeneration uint64
	RestartGeneration       uint64
}

// RuntimeStatus contains the observed runtime state.
type RuntimeStatus struct {
	State State
}

// NetworkInterface contains the host network values used by a runtime.
type NetworkInterface struct {
	NetworkNamespacePath string
	TapName              string
	MACAddress           string
	GuestIPAddress       string
	GatewayIPAddress     string
}

// Network converges and releases host network resources.
type Network interface {
	Ensure(context.Context, NetworkRequest) (NetworkInterface, error)
	Release(context.Context, NetworkReleaseRequest) error
}

// NetworkRequest contains the complete desired host network state.
type NetworkRequest struct {
	VirtualMachineID string
	UserID           uint32
	GroupID          uint32
	Configuration    NetworkConfiguration
}

// NetworkReleaseRequest identifies host network resources to remove.
type NetworkReleaseRequest struct {
	VirtualMachineID  string
	UserID            uint32
	WireGuardMeshIPv6 string
}

// Storage manages virtual machine disks.
type Storage interface {
	DiskUsage(context.Context, string) (DiskUsage, error)
	ResizeDisk(context.Context, string, int) error
	Release(context.Context, string) error
}

// DiskUsage contains disk size and allocation values.
type DiskUsage struct {
	SizeMiB int
	UsedMiB int
}

// Snapshots stages machine image snapshots.
type Snapshots interface {
	Stage(context.Context, SnapshotRequest) (StagedSnapshot, error)
}

// SnapshotRequest identifies the virtual machine and image to stage.
type SnapshotRequest struct {
	VirtualMachineID string
	ImageReference   string
}

// StagedSnapshot describes a staged machine image.
type StagedSnapshot struct {
	ID                     string
	SourceVirtualMachineID string
	RootfsSizeBytes        int64
	KernelSizeBytes        int64
}
