//go:build integration

package activity

// These values mirror the real VM network fixtures.
const (
	tapName         = "tap0"
	guestIPAddress  = "172.16.0.2"
	guestMACAddress = "06:00:ac:10:00:02"
)
