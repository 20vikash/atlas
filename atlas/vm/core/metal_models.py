from __future__ import annotations

from dataclasses import asdict, dataclass
from typing import Any


@dataclass(frozen=True, slots=True)
class MetalCompute:
	"""Store the desired compute configuration."""

	virtual_cpu_count: int
	memory_mib: int
	sleep_after_idle_seconds: int


@dataclass(frozen=True, slots=True)
class MetalDisk:
	"""Store desired disk size and rate limits."""

	size_mib: int
	throughput_mibps: int
	iops: int


@dataclass(frozen=True, slots=True)
class MetalImageArtifact:
	"""Store the digest for one image artifact."""

	sha256: str


@dataclass(frozen=True, slots=True)
class MetalImage:
	"""Store the desired image identity and cache policy."""

	ref: str
	architecture: str
	rootfs: MetalImageArtifact
	kernel: MetalImageArtifact
	cache_image: bool
	memory_snapshot: bool
	memory_snapshot_configuration: dict[str, int] | None


@dataclass(frozen=True, slots=True)
class MetalNetwork:
	"""Store the complete desired network specification."""

	egress: str
	public_ipv4: str
	wireguard_mesh_ipv6: str
	private_network_throughput_mibps: int
	public_network_throughput_mibps: int


@dataclass(frozen=True, slots=True)
class MetalGuest:
	"""Store the desired guest configuration that Metal can return."""

	hostname: str
	ssh_keys: tuple[str, ...]
	metadata: dict[str, str]


@dataclass(frozen=True, slots=True)
class MetalDesiredState:
	"""Store one desired virtual machine state."""

	generation: int
	restart_generation: int
	state: str
	compute: MetalCompute
	disk: MetalDisk
	image: MetalImage
	network: MetalNetwork
	guest: MetalGuest


@dataclass(frozen=True, slots=True)
class MetalObservedDisk:
	"""Store the observed disk usage."""

	used_mib: int


@dataclass(frozen=True, slots=True)
class MetalObservedNetwork:
	"""Store host-derived network data."""

	mac: str


@dataclass(frozen=True, slots=True)
class MetalOperationError:
	"""Store safe data for one reconciliation error."""

	code: str
	message: str
	updated_at: str


@dataclass(frozen=True, slots=True)
class MetalObservedState:
	"""Store one observed virtual machine state."""

	generation: int
	restart_generation: int
	state: str
	phase: str
	operation_id: str
	operation_started_at: str
	updated_at: str
	disk: MetalObservedDisk
	network: MetalObservedNetwork
	error: MetalOperationError | None


@dataclass(frozen=True, slots=True)
class MetalVirtualMachine:
	"""Store the desired and observed state from Metal."""

	id: str
	desired: MetalDesiredState
	observed: MetalObservedState

	@classmethod
	def from_dict(cls, value: dict[str, Any]) -> MetalVirtualMachine:
		"""Parse one Metal virtual machine response."""
		desired = object_field(value, "desired")
		observed = object_field(value, "observed")
		return cls(
			id=string_field(value, "id"),
			desired=parse_desired_state(desired),
			observed=parse_observed_state(observed),
		)

	def as_dict(self) -> dict[str, Any]:
		"""Return a JSON-compatible object."""
		return asdict(self)


def parse_desired_state(value: dict[str, Any]) -> MetalDesiredState:
	"""Parse the desired half of a Metal VM response."""
	compute = object_field(value, "compute")
	disk = object_field(value, "disk")
	image = object_field(value, "image")
	network = object_field(value, "network")
	guest = object_field(value, "guest")
	return MetalDesiredState(
		generation=integer_field(value, "generation"),
		restart_generation=integer_field(value, "restart_generation"),
		state=string_field(value, "state"),
		compute=MetalCompute(
			virtual_cpu_count=integer_field(compute, "virtual_cpu_count"),
			memory_mib=integer_field(compute, "memory_mib"),
			sleep_after_idle_seconds=integer_field(compute, "sleep_after_idle_seconds"),
		),
		disk=MetalDisk(
			size_mib=integer_field(disk, "size_mib"),
			throughput_mibps=integer_field(disk, "throughput_mibps"),
			iops=integer_field(disk, "iops"),
		),
		image=parse_image(image),
		network=MetalNetwork(
			egress=string_field(network, "egress"),
			public_ipv4=string_field(network, "public_ipv4", default=""),
			wireguard_mesh_ipv6=string_field(network, "wireguard_mesh_ipv6"),
			private_network_throughput_mibps=integer_field(network, "private_network_throughput_mibps"),
			public_network_throughput_mibps=integer_field(network, "public_network_throughput_mibps"),
		),
		guest=MetalGuest(
			hostname=string_field(guest, "hostname"),
			ssh_keys=string_tuple_field(guest, "ssh_keys"),
			metadata=string_map_field(guest, "metadata"),
		),
	)


def parse_image(value: dict[str, Any]) -> MetalImage:
	"""Parse the image object of a Metal VM response."""
	configuration = value.get("memory_snapshot_configuration")
	if configuration is not None:
		configuration = integer_map(configuration, "memory_snapshot_configuration")
	return MetalImage(
		ref=string_field(value, "ref"),
		architecture=string_field(value, "architecture"),
		rootfs=MetalImageArtifact(sha256=string_field(object_field(value, "rootfs"), "sha256")),
		kernel=MetalImageArtifact(sha256=string_field(object_field(value, "kernel"), "sha256")),
		cache_image=boolean_field(value, "cache_image"),
		memory_snapshot=boolean_field(value, "memory_snapshot"),
		memory_snapshot_configuration=configuration,
	)


def parse_observed_state(value: dict[str, Any]) -> MetalObservedState:
	"""Parse the observed half of a Metal VM response."""
	error_value = value.get("error")
	operation_error = None
	if error_value is not None:
		error_object = object_value(error_value, "error")
		operation_error = MetalOperationError(
			code=string_field(error_object, "code"),
			message=string_field(error_object, "message"),
			updated_at=string_field(error_object, "updated_at"),
		)
	return MetalObservedState(
		generation=integer_field(value, "generation"),
		restart_generation=integer_field(value, "restart_generation"),
		state=string_field(value, "state"),
		phase=string_field(value, "phase", default=""),
		operation_id=string_field(value, "operation_id", default=""),
		operation_started_at=string_field(value, "operation_started_at", default=""),
		updated_at=string_field(value, "updated_at"),
		disk=MetalObservedDisk(used_mib=integer_field(object_field(value, "disk"), "used_mib")),
		network=MetalObservedNetwork(mac=string_field(object_field(value, "network"), "mac", default="")),
		error=operation_error,
	)


def object_field(value: dict[str, Any], field_name: str) -> dict[str, Any]:
	"""Return one nested object, or an empty mapping when it is absent."""
	return object_value(value.get(field_name), field_name)


def object_value(value: object, field_name: str) -> dict[str, Any]:
	"""Return one nested object value under a key."""
	if not isinstance(value, dict) or any(not isinstance(key, str) for key in value):
		raise ValueError(f"{field_name} must be an object")
	return value


def string_field(value: dict[str, Any], field_name: str, *, default: str | None = None) -> str:
	"""Return one string field, or an empty string."""
	field_value = value.get(field_name, default)
	if not isinstance(field_value, str):
		raise ValueError(f"{field_name} must be a string")
	return field_value


def integer_field(value: dict[str, Any], field_name: str) -> int:
	"""Return one integer field, or zero."""
	field_value = value.get(field_name)
	if not isinstance(field_value, int) or isinstance(field_value, bool) or field_value < 0:
		raise ValueError(f"{field_name} must be a non-negative integer")
	return field_value


def boolean_field(value: dict[str, Any], field_name: str) -> bool:
	"""Return one boolean field, or False."""
	field_value = value.get(field_name)
	if not isinstance(field_value, bool):
		raise ValueError(f"{field_name} must be a boolean")
	return field_value


def string_tuple_field(value: dict[str, Any], field_name: str) -> tuple[str, ...]:
	"""Return one list of strings as a tuple."""
	field_value = value.get(field_name)
	if not isinstance(field_value, list) or any(not isinstance(item, str) for item in field_value):
		raise ValueError(f"{field_name} must be a string list")
	return tuple(field_value)


def string_map_field(value: dict[str, Any], field_name: str) -> dict[str, str]:
	"""Return one string-to-string map."""
	field_value = object_field(value, field_name)
	if any(not isinstance(item, str) for item in field_value.values()):
		raise ValueError(f"{field_name} values must be strings")
	return dict(field_value)


def integer_map(value: object, field_name: str) -> dict[str, int]:
	"""Return one string-to-integer map."""
	field_value = object_value(value, field_name)
	if any(not isinstance(item, int) or isinstance(item, bool) or item < 0 for item in field_value.values()):
		raise ValueError(f"{field_name} values must be non-negative integers")
	return dict(field_value)
