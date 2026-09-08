package vm

import (
	"context"
	"time"
)

// NetworkActivityRequest identifies the VM to read packet activity for.
type NetworkActivityRequest struct {
	VirtualMachineID string
	UserID           uint32
}

// NetworkActivity reports the last packet time of one VM.
type NetworkActivity struct {
	// LastSeenAt is the last packet time or attachment baseline.
	LastSeenAt time.Time
	// HasBeenSeen is true after the first packet.
	HasBeenSeen bool
	// LastPacketMonotonicNanoseconds is the raw eBPF time of the last packet.
	LastPacketMonotonicNanoseconds uint64
}

// NetworkActivityMonitor reads the last packet time of one VM.
type NetworkActivityMonitor interface {
	LastNetworkActivity(ctx context.Context, request NetworkActivityRequest) (NetworkActivity, error)
}

// NetworkWakeEvent reports a packet-triggered wake for one VM.
type NetworkWakeEvent struct {
	VirtualMachineID string
	UserID           uint32
	// PacketTime is the eBPF monotonic packet time.
	PacketTime uint64
}

// NetworkWakeMonitor arms packet wake and delivers wake events.
type NetworkWakeMonitor interface {
	// ArmNetworkWake arms one host-to-guest packet wake event.
	ArmNetworkWake(request NetworkActivityRequest) error
	// DisarmNetworkWake stops wake events for the VM. It is idempotent.
	DisarmNetworkWake(request NetworkActivityRequest) error
	// NetworkWakeEvents returns the shared read-only channel of wake events.
	NetworkWakeEvents() <-chan NetworkWakeEvent
}
