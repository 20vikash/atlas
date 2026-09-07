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
}

// NetworkActivityMonitor reads the last packet time of one VM. The VM manager
// consumes it to decide idle sleep in Phase 2.
type NetworkActivityMonitor interface {
	LastNetworkActivity(ctx context.Context, request NetworkActivityRequest) (NetworkActivity, error)
}
