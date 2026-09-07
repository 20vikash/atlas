package vm

import (
	"context"
	"testing"
)

// fakeNetworkActivityMonitor is the Phase 2 manager seam for activity reads.
type fakeNetworkActivityMonitor struct {
	activity NetworkActivity
	err      error
}

func (monitor *fakeNetworkActivityMonitor) LastNetworkActivity(context.Context, NetworkActivityRequest) (NetworkActivity, error) {
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
