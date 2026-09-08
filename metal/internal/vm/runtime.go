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
	// StopWithSleepSnapshot pauses the guest, saves a full snapshot, and terminates
	// Firecracker without a guest shutdown.
	StopWithSleepSnapshot
)

// StopOutcome reports the published snapshot produced by a warm stop. A normal
// shutdown leaves both fields zero.
type StopOutcome struct {
	MemorySnapshotGeneration uint64
	MemorySnapshotCreatedAt  time.Time
}

// SleepSnapshot describes a validated VM-local snapshot used to recover an
// interrupted warm stop.
type SleepSnapshot struct {
	Generation uint64
	CreatedAt  time.Time
}

// Runtime controls virtual machine processes and guest-facing operations.
type Runtime interface {
	Inspect(context.Context, RuntimeMachine) (RuntimeStatus, error)
	Start(context.Context, RuntimeMachine, StartMode) error
	Stop(context.Context, RuntimeMachine, StopMode) (StopOutcome, error)
	// InspectSleepSnapshot returns the newest valid sleep snapshot. It distinguishes
	// an absent snapshot from an invalid published artifact.
	InspectSleepSnapshot(context.Context, RuntimeMachine) (SleepSnapshot, error)
	// DiscardSleepSnapshot removes a VM's sleep snapshot and runtime unit so a
	// later start cold boots.
	DiscardSleepSnapshot(context.Context, RuntimeMachine) error
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
	// These generations identify the desired state for a snapshot restore and are
	// checked before the snapshot loads.
	SpecificationGeneration uint64
	RestartGeneration       uint64
}

// RuntimeStatus contains the observed runtime state.
type RuntimeStatus struct {
	State State
}
