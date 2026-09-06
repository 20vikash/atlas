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

func (c Config) vmDir(id string) string { return filepath.Join(c.MachinesDir, id) }
