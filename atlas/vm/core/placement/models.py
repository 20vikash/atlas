from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime


@dataclass(frozen=True, slots=True)
class Resources:
	"""CPU in millicores and memory and storage in MiB."""

	cpu_millicores: int
	memory_mib: int
	storage_mib: int

	def reserve(self, cpu_millicores: int, memory_mib: int, storage_mib: int) -> Resources:
		return Resources(
			cpu_millicores=max(self.cpu_millicores - cpu_millicores, 0),
			memory_mib=max(self.memory_mib - memory_mib, 0),
			storage_mib=max(self.storage_mib - storage_mib, 0),
		)


@dataclass(frozen=True, slots=True)
class PlacementDemand:
	"""The VM shape and identity visible to a placement strategy."""

	cpu_millicores: int
	memory_mib: int
	disk_mib: int
	architecture: str
	tenant_id: int
	is_sleepy: bool


@dataclass(frozen=True, slots=True)
class HostUsage:
	"""One fresh host sample with free capacity after local reservations."""

	name: str
	architecture: str
	is_sleepy: bool
	sample_created_at: datetime
	total: Resources
	free: Resources
	tenant_vm_count: int
	sleepy_reserved_memory_mib: int


@dataclass(frozen=True, slots=True)
class FleetUsage:
	"""Fresh ready hosts and the sum of their resource views."""

	hosts: tuple[HostUsage, ...]
	total: Resources
	free: Resources
	tenant_vm_count: int
	sleepy_reserved_memory_mib: int
