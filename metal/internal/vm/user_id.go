package vm

import "errors"

// UserIDRange is an inclusive range of host user IDs.
type UserIDRange struct {
	Min uint32
	Max uint32
}

// DefaultUserIDRange is the reserved host user ID range.
var DefaultUserIDRange = UserIDRange{Min: 100000, Max: 165535}

var errUserIDRangeExhausted = errors.New("vm user ID range exhausted")

// allocate returns the lowest ID in the range that no virtual machine holds.
// The lowest free ID is reused, so a host with churn does not walk the range.
func (userIDRange UserIDRange) allocate(usedUserIDs map[uint32]bool) (uint32, error) {
	for userID := uint64(userIDRange.Min); userID <= uint64(userIDRange.Max); userID++ {
		if !usedUserIDs[uint32(userID)] {
			return uint32(userID), nil
		}
	}

	return 0, errUserIDRangeExhausted
}
