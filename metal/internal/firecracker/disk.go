package firecracker

import (
	"github.com/frappe/atlas/metal/internal/firecracker/api"
	"github.com/frappe/atlas/metal/internal/vm"
)

// rateLimiterRefillMilliseconds sizes the token bucket over one second.
const rateLimiterRefillMilliseconds = 1000

// driveRateLimiter converts disk limits to Firecracker token buckets.
func driveRateLimiter(disk vm.Disk) *api.RateLimiter {
	limiter := api.RateLimiter{}
	if disk.ThroughputMiBps > 0 {
		limiter.Bandwidth = &api.TokenBucket{
			Size: int64(disk.ThroughputMiBps) * 1024 * 1024, RefillTime: rateLimiterRefillMilliseconds,
		}
	}
	if disk.IOPS > 0 {
		limiter.Ops = &api.TokenBucket{Size: int64(disk.IOPS), RefillTime: rateLimiterRefillMilliseconds}
	}
	if limiter.Bandwidth == nil && limiter.Ops == nil {
		return nil
	}
	return &limiter
}
