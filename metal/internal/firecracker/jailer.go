package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// apiSocketRelativePath is the firecracker API socket path relative to the chroot root.
const apiSocketRelativePath = "run/firecracker.socket"

// firecrackerLogRelativePath is the Firecracker log path inside the chroot.
const firecrackerLogRelativePath = "firecracker.log"

// chrootRoot returns the path where jailer builds the VM chroot. The base is the
// VM's own directory, so removing that directory takes the chroot with it, and
// the kernel hard link stays on one filesystem.
func (configuration Config) chrootRoot(id string) string {
	return filepath.Join(configuration.vmDir(id), filepath.Base(configuration.FirecrackerBin), id, "root")
}

// socketPath returns the short path metald dials for a VM's API socket.
func (configuration Config) socketPath(id string) string {
	return filepath.Join(configuration.SocketsDir, id+".sock")
}

// chrootSocketPath returns the real socket path inside the VM jail.
func (configuration Config) chrootSocketPath(id string) string {
	return filepath.Join(configuration.chrootRoot(id), apiSocketRelativePath)
}

// linkSocket creates the short socket symlink before Firecracker starts.
func (configuration Config) linkSocket(id string) error {
	if err := os.MkdirAll(configuration.SocketsDir, 0o700); err != nil {
		return err
	}
	link := configuration.socketPath(id)
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(configuration.chrootSocketPath(id), link)
}

// jailerArgs builds the argv the systemd unit runs: jailer flags, then "--",
// then the firecracker flags that run inside the chroot.
func (configuration Config) jailerArgs(id string, userID, groupID uint32, networkNamespacePath string) []string {
	return []string{
		"--id", id,
		"--exec-file", configuration.FirecrackerBin,
		"--uid", fmt.Sprint(userID),
		"--gid", fmt.Sprint(groupID),
		"--chroot-base-dir", configuration.vmDir(id),
		"--netns", networkNamespacePath,
		"--",
		"--api-sock", apiSocketRelativePath,
		"--log-path", firecrackerLogRelativePath,
		"--level", "Warn",
	}
}

// writeJailerEnv writes the EnvironmentFile the metal-vm@ template reads. systemd
// word-splits $JAILER_ARGS in ExecStart, so the args must not contain spaces.
func (configuration Config) writeJailerEnv(id string, arguments []string) error {
	line := "JAILER_ARGS=" + strings.Join(arguments, " ") + "\n"
	return platform.WriteFile(configuration.jailerEnvironmentPath(id), []byte(line), 0o640)
}

// jailerEnvironmentPath is the EnvironmentFile the systemd unit reads.
func (configuration Config) jailerEnvironmentPath(id string) string {
	return filepath.Join(configuration.vmDir(id), "jailer.env")
}
