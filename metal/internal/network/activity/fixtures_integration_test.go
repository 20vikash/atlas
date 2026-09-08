//go:build integration

package activity

// The integration tests build their own VM namespace. These values mirror the
// guest fixtures that the network allocator gives a real VM.
const (
	tapName         = "tap0"
	guestIPAddress  = "172.16.0.2"
	guestMACAddress = "06:00:ac:10:00:02"
)
