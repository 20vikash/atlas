package firecracker

import "path/filepath"

// Config contains Firecracker host and binary paths.
type Config struct {
	MachinesDir    string
	SocketsDir     string
	JailerBin      string
	FirecrackerBin string
}

// DefaultConfig returns the standard Firecracker host paths.
func DefaultConfig() Config {
	return Config{
		MachinesDir:    "/var/lib/metal/machines",
		SocketsDir:     "/run/metal",
		JailerBin:      "/usr/bin/jailer",
		FirecrackerBin: "/usr/bin/firecracker",
	}
}

// vmDir holds everything one VM owns on the host, so removing it removes the
// jail, the chroot, and the jailer environment together.
func (configuration Config) vmDir(id string) string {
	return filepath.Join(configuration.MachinesDir, id)
}
