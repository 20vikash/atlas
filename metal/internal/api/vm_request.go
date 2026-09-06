package api

import (
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"regexp"

	"github.com/frappe/atlas/metal/internal/vm"
)

const (
	maxResourceIDLength = 64
	maximumMemoryMiB    = (math.MaxInt - 128) / 2
)

var (
	imageRefPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	resourceIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	sha256DigestPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	wireGuardMeshPrefix = netip.MustParsePrefix("fdaa::/16")
)

type createRequest struct {
	Compute computeRequest `json:"compute"`
	Disk    diskRequest    `json:"disk"`
	Image   imageRequest   `json:"image"`
	Network networkRequest `json:"network"`
	Guest   guestRequest   `json:"guest"`
}

type computeRequest struct {
	VirtualCPUCount int `json:"virtual_cpu_count" minimum:"1"`
	MemoryMiB       int `json:"memory_mib" minimum:"1"`
}

type imageRequest struct {
	Ref                         string                              `json:"ref"`
	Architecture                string                              `json:"architecture"`
	Rootfs                      imageArtifactRequest                `json:"rootfs"`
	Kernel                      imageArtifactRequest                `json:"kernel"`
	CacheImage                  bool                                `json:"cache_image"`
	MemorySnapshot              bool                                `json:"memory_snapshot"`
	MemorySnapshotConfiguration *memorySnapshotConfigurationRequest `json:"memory_snapshot_configuration,omitempty"`
}

type memorySnapshotConfigurationRequest struct {
	VirtualCPUCount int `json:"virtual_cpu_count"`
	MemoryMiB       int `json:"memory_mib"`
	DiskMiB         int `json:"disk_mib"`
}

type imageArtifactRequest struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type diskRequest struct {
	SizeMiB         int `json:"size_mib" minimum:"1"`
	ThroughputMiBps int `json:"throughput_mibps"`
	IOPS            int `json:"iops"`
}

func (request diskRequest) validate() error {
	if request.SizeMiB <= 0 {
		return fmt.Errorf("disk.size_mib must be positive")
	}
	if request.ThroughputMiBps < 0 || request.IOPS < 0 {
		return fmt.Errorf("disk rate limits must not be negative")
	}
	return nil
}

func (request diskRequest) spec() vm.Disk {
	return vm.Disk{ThroughputMiBps: request.ThroughputMiBps, IOPS: request.IOPS}
}

type networkRequest struct {
	PublicIPv4                    string `json:"public_ipv4"`
	WireGuardMeshIPv6             string `json:"wireguard_mesh_ipv6"`
	PrivateNetworkThroughputMiBps int    `json:"private_network_throughput_mibps"`
	PublicNetworkThroughputMiBps  int    `json:"public_network_throughput_mibps"`
	Egress                        string `json:"egress"`
}

type guestRequest struct {
	Hostname string            `json:"hostname"`
	SSHKeys  []string          `json:"ssh_keys"`
	Metadata map[string]string `json:"metadata"`
	UserData string            `json:"user_data"`
}

type powerRequest struct {
	State string `json:"state" enums:"running,stopped,paused"`
}

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

func (request createRequest) spec() vm.Spec {
	return vm.Spec{
		VCPUs:     request.Compute.VirtualCPUCount,
		MemoryMiB: request.Compute.MemoryMiB,
		DiskMiB:   request.Disk.SizeMiB,
		Disk:      request.Disk.spec(),
		Image:     request.Image.specification(),
		Network:   request.Network.spec(),
		SSHKeys:   request.Guest.SSHKeys,
		Hostname:  request.Guest.Hostname,
		UserData:  request.Guest.UserData,
		Metadata:  request.Guest.Metadata,
	}
}

func (request computeRequest) validate() error {
	if request.VirtualCPUCount <= 0 || request.MemoryMiB <= 0 {
		return fmt.Errorf("compute values must be positive")
	}
	if request.MemoryMiB > maximumMemoryMiB {
		return fmt.Errorf("compute.memory_mib is too large")
	}
	return nil
}

func (request imageRequest) specification() vm.ImageRef {
	return vm.ImageRef{
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

func (request imageRequest) validate() error {
	if !validImageRef(request.Ref) {
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

func (request networkRequest) spec() vm.NetworkConfiguration {
	return vm.NetworkConfiguration{
		PublicIPv4:                    request.PublicIPv4,
		WireGuardMeshIPv6:             request.WireGuardMeshIPv6,
		PrivateNetworkThroughputMiBps: request.PrivateNetworkThroughputMiBps,
		PublicNetworkThroughputMiBps:  request.PublicNetworkThroughputMiBps,
		Egress:                        vm.Egress(request.Egress),
	}
}

func (request powerRequest) state() (vm.State, error) {
	state := vm.State(request.State)
	if state != vm.StateRunning && state != vm.StateStopped && state != vm.StatePaused {
		return "", fmt.Errorf("state must be running, stopped, or paused")
	}
	return state, nil
}

func validHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func validImageRef(value string) bool {
	return imageRefPattern.MatchString(value)
}

func validResourceID(value string) bool {
	return len(value) <= maxResourceIDLength && resourceIDPattern.MatchString(value)
}

func validSHA256Digest(value string) bool {
	return sha256DigestPattern.MatchString(value)
}
