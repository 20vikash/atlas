package reconciler

import (
	"context"
	"time"
)

// passScheduler runs one reconcile pass on an interval and on demand. Both
// reconcilers embed it, so they share one wake and shutdown behavior.
type passScheduler struct {
	interval time.Duration
	wake     chan struct{}
}

// newPassScheduler returns a scheduler that ticks at interval.
func newPassScheduler(interval time.Duration) passScheduler {
	return passScheduler{interval: interval, wake: make(chan struct{}, 1)}
}

// Wake requests a pass without blocking the caller. A pending request absorbs
// further calls, so many wakes collapse into one pass.
func (s *passScheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run calls pass once, then again on every tick or wake, until ctx is canceled.
func (s *passScheduler) run(ctx context.Context, pass func(context.Context)) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		pass(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
