package vm

import (
	"context"
	"testing"
)

// fakeNetworkActivityMonitor is the Phase 2 manager seam for activity reads. It
// returns each queued sequence value in order, then falls back to activity.
type fakeNetworkActivityMonitor struct {
	activity    NetworkActivity
	err         error
	lastRequest NetworkActivityRequest
	sequence    []NetworkActivity
}

func (monitor *fakeNetworkActivityMonitor) LastNetworkActivity(_ context.Context, request NetworkActivityRequest) (NetworkActivity, error) {
	monitor.lastRequest = request
	if len(monitor.sequence) > 0 {
		next := monitor.sequence[0]
		monitor.sequence = monitor.sequence[1:]
		return next, monitor.err
	}
	return monitor.activity, monitor.err
}

// The fake and the interface must stay in step for Phase 2.
var _ NetworkActivityMonitor = (*fakeNetworkActivityMonitor)(nil)

func TestNetworkActivityZeroValueHasNotBeenSeen(t *testing.T) {
	var activity NetworkActivity
	if activity.HasBeenSeen {
		t.Error("the zero value must report no packet seen")
	}
	if !activity.LastSeenAt.IsZero() {
		t.Error("the zero value must have a zero time")
	}
}
