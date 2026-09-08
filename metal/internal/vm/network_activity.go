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
	// LastSeenAt is the wall-clock time of the last packet, or the attachment
	// baseline when no packet has arrived.
	LastSeenAt time.Time
	// HasBeenSeen is true after at least one packet. It is false for a new
	// attachment without packet activity.
	HasBeenSeen bool
	// LastPacketMonotonicNanoseconds is the raw eBPF time of the last packet. It
	// changes only on packet arrival and gives sleep an exact traffic check.
	LastPacketMonotonicNanoseconds uint64
}

// NetworkActivityMonitor reads the last packet time of one VM. The manager uses
// it to decide whether the VM is idle.
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

// NetworkWakeMonitor arms and disarms packet wake and delivers wake events. The
// manager arms a sleeping VM, a reconciler consumes the shared event channel,
// and the channel closes when the monitor shuts down.
type NetworkWakeMonitor interface {
	// ArmNetworkWake arms one host-to-guest packet wake event. The VM must have an
	// existing activity attachment.
	ArmNetworkWake(request NetworkActivityRequest) error
	// DisarmNetworkWake stops wake events for the VM. It is idempotent.
	DisarmNetworkWake(request NetworkActivityRequest) error
	// NetworkWakeEvents returns the shared read-only channel of wake events.
	NetworkWakeEvents() <-chan NetworkWakeEvent
}
