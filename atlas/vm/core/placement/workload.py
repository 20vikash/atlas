"""Configuration and deterministic workload for placement replay."""

from __future__ import annotations

import random
from collections.abc import Mapping
from dataclasses import dataclass
from math import isfinite

from atlas.vm.core.placement.models import PlacementRequest, Resources

MILLISECONDS_PER_DAY = 86_400_000


@dataclass(frozen=True, slots=True)
class HostType:
	name: str
	architecture: str
	is_sleepy: bool
	resources: Resources


@dataclass(frozen=True, slots=True)
class VMShape:
	cpu_millicores: int
	memory_mib: int
	disk_mib: int
	weight: int
	architecture: str = "amd64"


@dataclass(frozen=True, slots=True)
class Scenario:
	host_types: Mapping[str, HostType]
	initial_hosts: tuple[str, ...]
	new_host_type: str
	shapes: tuple[VMShape, ...]
	arrivals_per_day: float
	mean_lifetime_hours: float
	mean_activity_minutes: float
	sleepy_fraction: float
	sleep_after_idle_seconds: int
	tenant_count: int
	sleepy_vm_overcommit_factor: float
	host_provision_seconds: float = 1800.0
	host_sync_seconds: float = 10.0
	boot_seconds: float = 2.5
	save_seconds: float = 5.5
	wake_seconds: float = 1.2

	def validate(self) -> None:
		if not self.host_types or self.new_host_type not in self.host_types:
			raise ValueError("new_host_type must name a configured host type")
		if any(name != host_type.name for name, host_type in self.host_types.items()):
			raise ValueError("host_types keys must match host type names")
		if any(name not in self.host_types for name in self.initial_hosts):
			raise ValueError("initial_hosts contains an unknown host type")
		if not self.shapes or any(
			min(shape.cpu_millicores, shape.memory_mib, shape.disk_mib, shape.weight) <= 0
			for shape in self.shapes
		):
			raise ValueError("shapes must have positive resources and weights")
		if any(
			min(host.resources.cpu_millicores, host.resources.memory_mib, host.resources.storage_mib) <= 0
			for host in self.host_types.values()
		):
			raise ValueError("host resources must be positive")
		if any(not isinstance(host.is_sleepy, bool) for host in self.host_types.values()):
			raise ValueError("host is_sleepy must be a boolean")
		if not 0 <= self.sleepy_fraction <= 1:
			raise ValueError("sleepy_fraction must be between zero and one")
		if self.tenant_count < 1 or self.sleep_after_idle_seconds < 1:
			raise ValueError("tenant_count and sleep_after_idle_seconds must be positive")
		if self.sleepy_vm_overcommit_factor < 1:
			raise ValueError("sleepy_vm_overcommit_factor must be at least one")
		if not all(
			isfinite(value)
			for value in (
				self.arrivals_per_day,
				self.mean_lifetime_hours,
				self.mean_activity_minutes,
				self.sleepy_fraction,
				self.sleepy_vm_overcommit_factor,
				self.host_provision_seconds,
				self.host_sync_seconds,
				self.boot_seconds,
				self.save_seconds,
				self.wake_seconds,
			)
		):
			raise ValueError("scenario rates and durations must be finite")
		if (
			min(
				self.arrivals_per_day,
				self.mean_lifetime_hours,
				self.mean_activity_minutes,
				self.host_provision_seconds,
				self.boot_seconds,
				self.save_seconds,
				self.wake_seconds,
			)
			<= 0
			or self.host_sync_seconds < 0
		):
			raise ValueError("rates and durations must be positive; host_sync_seconds may be zero")
		if any(
			not any(
				host.architecture == shape.architecture
				and host.resources.memory_mib >= shape.memory_mib
				and host.resources.storage_mib >= shape.disk_mib
				for host in self.host_types.values()
			)
			for shape in self.shapes
		):
			raise ValueError("each VM shape must fit a configured host type")


@dataclass(frozen=True, slots=True)
class VMWorkload:
	identifier: int
	request: PlacementRequest
	arrive_ms: int
	depart_ms: int
	activity_ms: tuple[int, ...]


def generate_workload(scenario: Scenario, days: int, seed: int) -> tuple[VMWorkload, ...]:
	"""Generate every external event once, before comparing strategies."""
	if days < 1:
		raise ValueError("days must be positive")
	scenario.validate()
	rng = random.Random(seed)
	horizon_ms = days * MILLISECONDS_PER_DAY
	arrive_ms = 0
	workload: list[VMWorkload] = []
	while True:
		arrive_ms += max(1, round(rng.expovariate(scenario.arrivals_per_day / MILLISECONDS_PER_DAY)))
		if arrive_ms >= horizon_ms:
			break
		shape = rng.choices(scenario.shapes, weights=[item.weight for item in scenario.shapes])[0]
		depart_ms = arrive_ms + max(1, round(rng.expovariate(1 / (scenario.mean_lifetime_hours * 3_600_000))))
		request = PlacementRequest(
			shape.cpu_millicores,
			shape.memory_mib,
			shape.disk_mib,
			shape.architecture,
			rng.randrange(1, scenario.tenant_count + 1),
			rng.random() < scenario.sleepy_fraction,
		)
		activity_ms: list[int] = []
		if request.is_sleepy:
			activity_at_ms = arrive_ms
			while True:
				activity_at_ms += max(
					1, round(rng.expovariate(1 / (scenario.mean_activity_minutes * 60_000)))
				)
				if activity_at_ms >= min(depart_ms, horizon_ms):
					break
				activity_ms.append(activity_at_ms)
		workload.append(VMWorkload(len(workload), request, arrive_ms, depart_ms, tuple(activity_ms)))
	return tuple(workload)
