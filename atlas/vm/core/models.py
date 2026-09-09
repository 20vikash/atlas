from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

import frappe

from atlas.atlas.core.parsing import strict_bool

EGRESS_MODES = ("uplink", "mesh", "none")


@dataclass(frozen=True, slots=True)
class VirtualMachineCreateRequest:
	"""Store the validated values for one virtual machine request."""

	virtual_machine_image: str
	virtual_cpu_count: int
	memory_mib: int
	disk_mib: int
	tenant_id: int
	is_privileged: bool = False
	hostname: str = ""
	ssh_keys: tuple[str, ...] = ()
	user_data: str = ""
	egress: str = "uplink"
	is_sleepy: bool = False
	idle_timeout_seconds: int = 0
	disk_throughput_mibps: int = 0
	disk_iops: int = 0
	private_network_throughput_mibps: int = 0
	public_network_throughput_mibps: int = 0
	server_ip_address: str | None = None
	metadata: dict[str, str] = field(default_factory=dict)

	@classmethod
	def from_value(cls, value: str | dict[str, Any]) -> VirtualMachineCreateRequest:
		"""Parse and validate one virtual machine request."""
		payload = frappe.parse_json(value) if isinstance(value, str) else value
		if not isinstance(payload, dict):
			raise ValueError("Virtual Machine request must be a JSON object.")

		image = payload.get("virtual_machine_image")
		if not isinstance(image, str) or not image:
			raise ValueError("Virtual Machine Image is required.")

		virtual_cpu_count = cls.positive_integer(payload, "vcpus", "vCPUs")
		memory_mib = cls.positive_integer(payload, "memory_mib", "Memory")
		disk_mib = cls.positive_integer(payload, "disk_mib", "Disk")
		tenant_id = payload.get("tenant_id")
		if not isinstance(tenant_id, int) or isinstance(tenant_id, bool) or not 0 <= tenant_id <= 0xFFFFFFFF:
			raise ValueError("Tenant ID must be a 32-bit unsigned integer.")

		egress = payload.get("egress") or "uplink"
		if egress not in EGRESS_MODES:
			raise ValueError("Egress must be uplink, mesh, or none.")

		public_network_throughput_mibps = cls.non_negative_integer(payload, "public_network_throughput_mibps")
		server_ip_address = payload.get("server_ip_address") or None
		if egress != "uplink" and server_ip_address:
			raise ValueError("A public IPv4 address requires uplink egress.")

		return cls(
			virtual_machine_image=image,
			virtual_cpu_count=virtual_cpu_count,
			memory_mib=memory_mib,
			disk_mib=disk_mib,
			tenant_id=tenant_id,
			is_privileged=strict_bool(payload.get("is_privileged"), "is_privileged"),
			hostname=str(payload.get("hostname") or ""),
			ssh_keys=cls.ssh_keys_tuple(payload),
			user_data=str(payload.get("user_data") or ""),
			egress=egress,
			is_sleepy=strict_bool(payload.get("is_sleepy"), "is_sleepy"),
			idle_timeout_seconds=cls.non_negative_integer(payload, "idle_timeout_seconds"),
			disk_throughput_mibps=cls.non_negative_integer(payload, "disk_throughput_mibps"),
			disk_iops=cls.non_negative_integer(payload, "disk_iops"),
			private_network_throughput_mibps=cls.non_negative_integer(
				payload, "private_network_throughput_mibps"
			),
			public_network_throughput_mibps=public_network_throughput_mibps,
			server_ip_address=server_ip_address,
			metadata=cls.metadata_map(payload),
		)

	@staticmethod
	def ssh_keys_tuple(payload: dict[str, Any]) -> tuple[str, ...]:
		"""Return the authorized keys from a list or from a newline-separated block."""
		value = payload.get("ssh_keys") or []
		if isinstance(value, str):
			value = value.splitlines()
		if not isinstance(value, list) or any(not isinstance(key, str) for key in value):
			raise ValueError("SSH keys must be a list of strings.")

		return tuple(key.strip() for key in value if key.strip())

	@staticmethod
	def non_negative_integer(payload: dict[str, Any], field_name: str) -> int:
		"""Return one optional non-negative integer."""
		value = payload.get(field_name) or 0
		if not isinstance(value, int) or isinstance(value, bool) or value < 0:
			raise ValueError(f"{field_name} must be a non-negative integer.")
		return value

	@staticmethod
	def metadata_map(payload: dict[str, Any]) -> dict[str, str]:
		"""Return validated metadata with clean keys."""
		value = payload.get("metadata") or {}
		if not isinstance(value, dict):
			raise ValueError("Metadata must be a string-to-string map.")

		metadata: dict[str, str] = {}
		for key, item in value.items():
			if not isinstance(key, str) or not isinstance(item, str):
				raise ValueError("Metadata keys and values must be strings.")
			key = key.strip()
			if not key:
				raise ValueError("Metadata key cannot be empty.")
			metadata[key] = item
		return metadata

	@staticmethod
	def positive_integer(payload: dict[str, Any], field_name: str, label: str) -> int:
		"""Return one required positive integer."""
		value = payload.get(field_name)
		if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
			raise ValueError(f"{label} must be a positive integer.")
		return value
