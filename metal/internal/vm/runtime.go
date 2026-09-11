package vm

import "context"

// Runtime controls virtual machine processes and guest-facing operations.
type Runtime interface {
	Inspect(context.Context, RuntimeMachine) (RuntimeStatus, error)
	Start(context.Context, RuntimeMachine) error
	// ColdStart boots the disk without restoring warm memory.
	ColdStart(context.Context, RuntimeMachine) error
	Stop(context.Context, RuntimeMachine) error
	SaveAndStop(context.Context, RuntimeMachine) error
	Restore(context.Context, RuntimeMachine) error
	RestorePaused(context.Context, RuntimeMachine) error
	DeleteSavedState(context.Context, RuntimeMachine) error
	Pause(context.Context, RuntimeMachine) error
	Resume(context.Context, RuntimeMachine) error
	Remove(context.Context, RuntimeMachine) error
	RefreshMetadata(context.Context, RuntimeMachine) error
	RefreshDisk(context.Context, RuntimeMachine) error
	// LimitDiskThroughput applies a temporary combined read and write limit.
	LimitDiskThroughput(ctx context.Context, machine RuntimeMachine, throughputMiBps int) error
	ConnectSSH(context.Context, RuntimeMachine) (SSHConnection, error)
}

// RuntimeMachine contains the complete input for one runtime operation.
type RuntimeMachine struct {
	ID               string
	UserID           uint32
	GroupID          uint32
	Specification    Specification
	NetworkInterface NetworkInterface
	// These generations identify the desired state for a snapshot restore and are
	// checked before the snapshot loads.
	SpecificationGeneration uint64
	RestartGeneration       uint64
}

// RuntimeStatus contains the observed runtime state.
type RuntimeStatus struct {
	State         State
	HasSavedState bool
}
