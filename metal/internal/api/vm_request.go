package api

import (
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

// Bounds on caller-supplied values.
const (
	// maximumResourceIDLength keeps an ID usable as a path, ZFS dataset, and
	// systemd unit instance name.
	maximumResourceIDLength = 64

	// maximumMemoryMiB prevents unit memory overflow. The unit limit is twice the
	// guest size plus fixed overhead.
	maximumMemoryMiB             = (math.MaxInt - 128) / 2
	maximumSleepAfterIdleSeconds = int64(math.MaxInt64) / int64(time.Second)
)

// Patterns that keep caller-supplied values usable as host paths and names.
var (
	imageReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	resourceIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	sha256DigestPattern   = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

	// wireGuardMeshPrefix is the only block a VM mesh address may fall in.
	wireGuardMeshPrefix = netip.MustParsePrefix("fdaa::/16")
)

// createRequest is the complete desired specification of a new VM. Each group
// is required because creation stores state instead of merging it.
type createRequest struct {
	Compute computeRequest `json:"compute"`
	Disk    diskRequest    `json:"disk"`
	Image   imageRequest   `json:"image"`
	Network networkRequest `json:"network"`
	Guest   guestRequest   `json:"guest"`
}

// computeRequest is the complete compute configuration.
type computeRequest struct {
	VirtualCPUCount       int `json:"virtual_cpu_count" minimum:"1"`
	MemoryMiB             int `json:"memory_mib" minimum:"1"`
	SleepAfterIdleSeconds int `json:"sleep_after_idle_seconds" minimum:"0"`
}

// imageRequest identifies boot content and how the host should keep it.
type imageRequest struct {
	Ref                         string                              `json:"ref"`
	Architecture                string                              `json:"architecture"`
	Rootfs                      imageArtifactRequest                `json:"rootfs"`
	Kernel                      imageArtifactRequest                `json:"kernel"`
	CacheImage                  bool                                `json:"cache_image"`
	MemorySnapshot              bool                                `json:"memory_snapshot"`
	MemorySnapshotConfiguration *memorySnapshotConfigurationRequest `json:"memory_snapshot_configuration,omitempty"`
}

// memorySnapshotConfigurationRequest is the exact VM shape a warm image serves.
type memorySnapshotConfigurationRequest struct {
	VirtualCPUCount int `json:"virtual_cpu_count"`
	MemoryMiB       int `json:"memory_mib"`
	DiskMiB         int `json:"disk_mib"`
}

// imageArtifactRequest is one downloadable artifact and its digest.
type imageArtifactRequest struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// diskRequest is the complete disk size and its rate limits.
type diskRequest struct {
	SizeMiB         int `json:"size_mib" minimum:"1"`
	ThroughputMiBps int `json:"throughput_mibps"`
	IOPS            int `json:"iops"`
}

// validate rejects a disk that is not usable.
func (request diskRequest) validate() error {
	if request.SizeMiB <= 0 {
		return fmt.Errorf("disk.size_mib must be positive")
	}
	if request.ThroughputMiBps < 0 || request.IOPS < 0 {
		return fmt.Errorf("disk rate limits must not be negative")
	}
	return nil
}

// specification converts the request into the domain disk limits.
func (request diskRequest) specification() vm.Disk {
	return vm.Disk{ThroughputMiBps: request.ThroughputMiBps, IOPS: request.IOPS}
}

// networkRequest is the complete desired VM network.
type networkRequest struct {
	PublicIPv4                    string `json:"public_ipv4"`
	WireGuardMeshIPv6             string `json:"wireguard_mesh_ipv6"`
	PrivateNetworkThroughputMiBps int    `json:"private_network_throughput_mibps"`
	PublicNetworkThroughputMiBps  int    `json:"public_network_throughput_mibps"`
	Egress                        string `json:"egress"`
}

// guestRequest carries the values published to the guest through MMDS.
type guestRequest struct {
	Hostname string            `json:"hostname"`
	SSHKeys  []string          `json:"ssh_keys"`
	Metadata map[string]string `json:"metadata"`
	UserData string            `json:"user_data"`
}

// powerRequest is the desired power state.
type powerRequest struct {
	State string `json:"state" enums:"running,stopped,paused"`
}

// validate checks every group and the guest values a create carries.
func (request createRequest) validate() error {
	if err := request.Compute.validate(); err != nil {
		return err
	}
	if err := request.Disk.validate(); err != nil {
		return err
	}
	if err := request.Image.validate(); err != nil {
		return err
	}
	if err := request.Network.validate(); err != nil {
		return err
	}

	if _, err := validateSSHKeys(request.Guest.SSHKeys); err != nil {
		return err
	}
	return validateMetadata(request.Guest.Metadata)
}

// specification converts the request into the domain VM specification.
func (request createRequest) specification() vm.Specification {
	return vm.Specification{
		VirtualCPUCount:       request.Compute.VirtualCPUCount,
		MemoryMiB:             request.Compute.MemoryMiB,
		SleepAfterIdleSeconds: request.Compute.SleepAfterIdleSeconds,
		DiskMiB:               request.Disk.SizeMiB,
		Disk:                  request.Disk.specification(),
		Image:                 request.Image.specification(),
		Network:               request.Network.specification(),
		SSHKeys:               request.Guest.SSHKeys,
		Hostname:              request.Guest.Hostname,
		UserData:              request.Guest.UserData,
		Metadata:              request.Guest.Metadata,
	}
}

// validate rejects a compute shape the host cannot represent.
func (request computeRequest) validate() error {
	if request.VirtualCPUCount <= 0 || request.MemoryMiB <= 0 {
		return fmt.Errorf("compute values must be positive")
	}
	if request.MemoryMiB > maximumMemoryMiB {
		return fmt.Errorf("compute.memory_mib is too large")
	}
	if request.SleepAfterIdleSeconds < 0 {
		return fmt.Errorf("compute.sleep_after_idle_seconds must not be negative")
	}
	if int64(request.SleepAfterIdleSeconds) > maximumSleepAfterIdleSeconds {
		return fmt.Errorf("compute.sleep_after_idle_seconds is too large")
	}
	return nil
}

// specification converts the request into the domain image.
func (request imageRequest) specification() vm.Image {
	return vm.Image{
		Name:                        request.Ref,
		Architecture:                request.Architecture,
		RootfsURL:                   request.Rootfs.URL,
		RootfsSHA256:                request.Rootfs.SHA256,
		KernelURL:                   request.Kernel.URL,
		KernelSHA256:                request.Kernel.SHA256,
		CacheImage:                  request.CacheImage,
		MemorySnapshot:              request.MemorySnapshot,
		MemorySnapshotConfiguration: request.MemorySnapshotConfiguration.specification(),
	}
}

// validate rejects an image that cannot be fetched or verified.
func (request imageRequest) validate() error {
	if !validImageReference(request.Ref) {
		return fmt.Errorf("image.ref must match [A-Za-z0-9._:-] and start alphanumeric")
	}
	if request.Architecture == "" {
		return fmt.Errorf("image.architecture is required")
	}
	if !validHTTPURL(request.Rootfs.URL) || !validHTTPURL(request.Kernel.URL) {
		return fmt.Errorf("image rootfs and kernel URLs must use HTTP or HTTPS")
	}
	if !validSHA256Digest(request.Rootfs.SHA256) || !validSHA256Digest(request.Kernel.SHA256) {
		return fmt.Errorf("image rootfs and kernel SHA-256 values are invalid")
	}
	if request.MemorySnapshot {
		configuration := request.MemorySnapshotConfiguration
		if configuration == nil || configuration.VirtualCPUCount <= 0 || configuration.MemoryMiB <= 0 || configuration.DiskMiB <= 0 {
			return fmt.Errorf("image.memory_snapshot_configuration must contain positive values")
		}
	}

	return nil
}

// specification converts the request into the domain warm image shape.
func (request *memorySnapshotConfigurationRequest) specification() *vm.MemorySnapshotConfiguration {
	if request == nil {
		return nil
	}

	return &vm.MemorySnapshotConfiguration{
		VirtualCPUCount: request.VirtualCPUCount,
		MemoryMiB:       request.MemoryMiB,
		DiskMiB:         request.DiskMiB,
	}
}

// validate rejects a network the host cannot build.
func (request networkRequest) validate() error {
	if request.PrivateNetworkThroughputMiBps < 0 || request.PublicNetworkThroughputMiBps < 0 {
		return fmt.Errorf("network throughput values must not be negative")
	}

	wireGuardAddress, err := netip.ParseAddr(request.WireGuardMeshIPv6)
	if err != nil || !wireGuardMeshPrefix.Contains(wireGuardAddress) {
		return fmt.Errorf("network.wireguard_mesh_ipv6 must be in fdaa::/16")
	}
	egress := vm.Egress(request.Egress)
	if !egress.IsValid() {
		return fmt.Errorf("network.egress must be %s, %s, or %s", vm.EgressUplink, vm.EgressMesh, vm.EgressNone)
	}
	if request.PublicIPv4 == "" {
		return nil
	}

	publicAddress, err := netip.ParseAddr(request.PublicIPv4)
	if err != nil || !publicAddress.Is4() {
		return fmt.Errorf("network.public_ipv4 must be an IPv4 address")
	}
	if !egress.HasInternetPath() {
		return fmt.Errorf("network.public_ipv4 requires %s egress", vm.EgressUplink)
	}
	return nil
}

// specification converts the request into the domain network configuration.
func (request networkRequest) specification() vm.NetworkConfiguration {
	return vm.NetworkConfiguration{
		PublicIPv4:                    request.PublicIPv4,
		WireGuardMeshIPv6:             request.WireGuardMeshIPv6,
		PrivateNetworkThroughputMiBps: request.PrivateNetworkThroughputMiBps,
		PublicNetworkThroughputMiBps:  request.PublicNetworkThroughputMiBps,
		Egress:                        vm.Egress(request.Egress),
	}
}

// state returns the requested power state, or an error for any other value.
func (request powerRequest) state() (vm.State, error) {
	state := vm.State(request.State)
	if state != vm.StateRunning && state != vm.StateStopped && state != vm.StatePaused {
		return "", fmt.Errorf("state must be running, stopped, or paused")
	}
	return state, nil
}

// validHTTPURL reports whether value is an absolute http or https URL.
func validHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// validImageReference reports whether value is usable as a directory and ZFS
// dataset name.
func validImageReference(value string) bool {
	return imageReferencePattern.MatchString(value)
}

// validResourceID reports whether value is usable as a VM or snapshot identifier.
func validResourceID(value string) bool {
	return len(value) <= maximumResourceIDLength && resourceIDPattern.MatchString(value)
}

// validSHA256Digest reports whether value is a full hexadecimal SHA-256 digest.
func validSHA256Digest(value string) bool {
	return sha256DigestPattern.MatchString(value)
}
