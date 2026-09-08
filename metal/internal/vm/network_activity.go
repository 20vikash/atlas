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
	// LastSeenAt is the wall-clock time of the last packet. When HasBeenSeen is
	// false, it is the attachment baseline time instead.
	LastSeenAt time.Time
	// HasBeenSeen is true after at least one packet. It is false for a new
	// attachment that has not seen a packet yet.
	HasBeenSeen bool
	// LastPacketMonotonicNanoseconds is the raw eBPF monotonic time of the last
	// packet, or 0 when HasBeenSeen is false. It only changes when a packet
	// arrives, so it gives an exact traffic check for the sleep abort, free of the
	// wall-clock read jitter in LastSeenAt.
	LastPacketMonotonicNanoseconds uint64
}

// NetworkActivityMonitor reads the last packet time of one VM. The VM manager
// consumes it to decide idle sleep in Phase 2.
type NetworkActivityMonitor interface {
	LastNetworkActivity(ctx context.Context, request NetworkActivityRequest) (NetworkActivity, error)
}

// NetworkWakeEvent reports that a host-to-guest packet reached a VM whose wake
// notification was armed. The manager wakes the VM from its sleep snapshot.
type NetworkWakeEvent struct {
	VirtualMachineID string
	UserID           uint32
	// PacketTime is the monotonic nanosecond time the eBPF program recorded for
	// the packet. It is not a wall-clock time.
	PacketTime uint64
}

// NetworkWakeMonitor arms and disarms packet-triggered wake for one VM and
// delivers wake events. The manager arms a VM when it sleeps and disarms it when
// it wakes. A reconciler consumes the wake event channel. The channel is closed
// once, when the monitor shuts down.
type NetworkWakeMonitor interface {
	// ArmNetworkWake makes the next host-to-guest packet for the VM produce one
	// wake event. It needs an existing activity attachment for the VM.
	ArmNetworkWake(request NetworkActivityRequest) error
	// DisarmNetworkWake stops wake events for the VM. It is idempotent.
	DisarmNetworkWake(request NetworkActivityRequest) error
	// NetworkWakeEvents returns the shared read-only channel of wake events.
	NetworkWakeEvents() <-chan NetworkWakeEvent
}
