"""Offline, event-driven replay of registered placement strategies."""

from __future__ import annotations

import heapq
from collections import deque
from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from itertools import count

from atlas.vm.core.placement.models import FleetUsage, HostUsage, PlacementRequest, Resources
from atlas.vm.core.placement.workload import MILLISECONDS_PER_DAY, HostType, Scenario, VMWorkload

RETRY_MILLISECONDS = 15_000
RATE_WINDOW_MILLISECONDS = 300_000
SAMPLE_EPOCH = datetime(2026, 1, 1, tzinfo=UTC)


@dataclass(slots=True)
class _Host:
	name: str
	host_type: HostType
	ready: bool
	virtual_machines: set[int] = field(default_factory=set)


@dataclass(slots=True)
class _VM:
	workload: VMWorkload
	state: str = "waiting"
	host_name: str | None = None
	sleep_due_ms: int | None = None
	save_due_ms: int | None = None


@dataclass(frozen=True, slots=True)
class Result:
	strategy: str
	requested: int
	placed: int
	delayed_placements: int
	rejected: int
	expired_unplaced: int
	pending_at_end: int
	wait_p50_seconds: float
	wait_p95_seconds: float
	wait_max_seconds: float
	hosts_at_end: int
	new_hosts: int
	host_hours: float
	ready_memory_utilization: float
	ready_storage_utilization: float
	ready_cpu_allocation: float
	sleeps: int
	wakes: int
	wake_memory_shortfalls: int
	peak_wake_shortfall_mib: int


class SimulatedPlacementAPI:
	"""The strategy-facing surface of PlacementAPI, backed by simulation state."""

	def __init__(self, simulation: Simulation, virtual_machine: _VM) -> None:
		self._simulation = simulation
		self._selected_host_name: str | None = None
		self._pending_hosts: set[str] = set()
		self.request = virtual_machine.workload.request
		self.usage = simulation._usage(self.request.tenant_id)
		self.sleepy_vm_overcommit_factor = simulation.scenario.sleepy_vm_overcommit_factor

	def placement_rate(self, host_name: str | None = None) -> float:
		return self._simulation._placement_rate(host_name)

	def select(self, host_name: str) -> bool:
		if self._selected_host_name is not None:
			raise RuntimeError("A placement host is already selected.")
		if host_name not in {host.name for host in self.usage.hosts}:
			return False
		host = self._simulation._hosts[host_name]
		free = self._simulation._free(host)
		if (
			not host.ready
			or host.host_type.architecture != self.request.architecture
			or free.memory_mib < self.request.memory_mib
			or free.storage_mib < self.request.disk_mib
		):
			return False
		self._selected_host_name = host_name
		return True

	def spawn_host(self, host_type: str | None = None, *, count: int = 1) -> tuple[str, ...]:
		if isinstance(count, bool) or not isinstance(count, int) or count < 1:
			raise ValueError("count must be a positive integer")
		selected_type = host_type or self._simulation.scenario.new_host_type
		catalog = self._simulation.scenario.host_types.get(selected_type)
		if catalog is None:
			raise ValueError(f"Unknown host type: {selected_type}")
		if (
			catalog.architecture != self.request.architecture
			or catalog.resources.memory_mib < self.request.memory_mib
			or catalog.resources.storage_mib < self.request.disk_mib
		):
			raise ValueError(f"Host type {selected_type} cannot hold this VM shape")
		names = [
			host.name
			for host in self._simulation._hosts.values()
			if not host.ready and host.host_type.name == selected_type
		]
		for _ in range(max(0, count - len(names))):
			names.append(self._simulation._spawn(catalog))
		self._pending_hosts.update(names)
		return tuple(names)


class Simulation:
	"""Own simulated hosts, VM state, clock, and event queue for one strategy."""

	def __init__(self, scenario: Scenario, workload: tuple[VMWorkload, ...], days: int) -> None:
		self.scenario = scenario
		self._horizon_ms = days * MILLISECONDS_PER_DAY
		self._now_ms = 0
		self._hosts: dict[str, _Host] = {}
		self._virtual_machines = {item.identifier: _VM(item) for item in workload}
		self._events: list[tuple[int, int, str, int | str]] = []
		self._event_sequence = count()
		self._placements: deque[tuple[int, str]] = deque()
		self._wait_ms: list[int] = []
		self._rejected = 0
		self._expired_unplaced = 0
		self._sleeps = 0
		self._wakes = 0
		self._wake_shortfalls = 0
		self._peak_wake_shortfall_mib = 0
		self._ready_capacity_area = Resources(0, 0, 0)
		self._ready_used_area = Resources(0, 0, 0)
		self._host_milliseconds = 0
		for host_type_name in scenario.initial_hosts:
			self._add_host(scenario.host_types[host_type_name], ready=True)
		for item in workload:
			self._schedule(item.arrive_ms, "arrive", item.identifier)
			self._schedule(item.depart_ms, "depart", item.identifier)
			for activity_ms in item.activity_ms:
				self._schedule(activity_ms, "activity", item.identifier)

	def _schedule(self, at_ms: int, kind: str, identifier: int | str) -> None:
		if at_ms <= self._horizon_ms:
			heapq.heappush(self._events, (at_ms, next(self._event_sequence), kind, identifier))

	def _add_host(self, host_type: HostType, *, ready: bool) -> str:
		name = f"host-{len(self._hosts) + 1:04d}"
		self._hosts[name] = _Host(name, host_type, ready)
		return name

	def _spawn(self, host_type: HostType) -> str:
		name = self._add_host(host_type, ready=False)
		delay_ms = round((self.scenario.host_provision_seconds + self.scenario.host_sync_seconds) * 1000)
		self._schedule(self._now_ms + delay_ms, "host_ready", name)
		return name

	def _used(self, host: _Host) -> Resources:
		virtual_machines = (self._virtual_machines[identifier] for identifier in host.virtual_machines)
		cpu_millicores = memory_mib = storage_mib = 0
		for virtual_machine in virtual_machines:
			request = virtual_machine.workload.request
			cpu_millicores += request.cpu_millicores
			storage_mib += request.disk_mib
			if virtual_machine.state != "asleep":
				memory_mib += request.memory_mib
		return Resources(cpu_millicores, memory_mib, storage_mib)

	def _free(self, host: _Host) -> Resources:
		used = self._used(host)
		return host.host_type.resources.reserve(used.cpu_millicores, used.memory_mib, used.storage_mib)

	def _usage(self, tenant_id: int) -> FleetUsage:
		at = SAMPLE_EPOCH + timedelta(milliseconds=self._now_ms)
		hosts = tuple(
			HostUsage(
				name=host.name,
				architecture=host.host_type.architecture,
				is_sleepy=host.host_type.is_sleepy,
				sample_created_at=at,
				total=host.host_type.resources,
				free=self._free(host),
				tenant_vm_count=sum(
					self._virtual_machines[identifier].workload.request.tenant_id == tenant_id
					for identifier in host.virtual_machines
				),
				sleepy_reserved_memory_mib=sum(
					self._virtual_machines[identifier].workload.request.memory_mib
					for identifier in host.virtual_machines
					if self._virtual_machines[identifier].workload.request.is_sleepy
				),
			)
			for host in self._hosts.values()
			if host.ready
		)
		return FleetUsage(
			hosts,
			Resources(
				sum(host.total.cpu_millicores for host in hosts),
				sum(host.total.memory_mib for host in hosts),
				sum(host.total.storage_mib for host in hosts),
			),
			Resources(
				sum(host.free.cpu_millicores for host in hosts),
				sum(host.free.memory_mib for host in hosts),
				sum(host.free.storage_mib for host in hosts),
			),
			sum(host.tenant_vm_count for host in hosts),
			sum(host.sleepy_reserved_memory_mib for host in hosts),
		)

	def _placement_rate(self, host_name: str | None) -> float:
		while self._placements and self._placements[0][0] < self._now_ms - RATE_WINDOW_MILLISECONDS:
			self._placements.popleft()
		return sum(host_name is None or name == host_name for _, name in self._placements) / 5

	def _place(self, virtual_machine: _VM, strategy: Callable[[SimulatedPlacementAPI], None]) -> None:
		if virtual_machine.state != "waiting" or self._now_ms >= virtual_machine.workload.depart_ms:
			return
		api = SimulatedPlacementAPI(self, virtual_machine)
		strategy(api)
		if api._selected_host_name is not None:
			host = self._hosts[api._selected_host_name]
			host.virtual_machines.add(virtual_machine.workload.identifier)
			virtual_machine.host_name = host.name
			virtual_machine.state = "booting"
			self._placements.append((self._now_ms, host.name))
			self._wait_ms.append(self._now_ms - virtual_machine.workload.arrive_ms)
			self._schedule(
				self._now_ms + round(self.scenario.boot_seconds * 1000),
				"boot",
				virtual_machine.workload.identifier,
			)
		elif api._pending_hosts:
			self._schedule(self._now_ms + RETRY_MILLISECONDS, "retry", virtual_machine.workload.identifier)
		else:
			virtual_machine.state = "rejected"
			self._rejected += 1

	def _sleep(self, virtual_machine: _VM) -> None:
		virtual_machine.sleep_due_ms = self._now_ms + self.scenario.sleep_after_idle_seconds * 1000
		self._schedule(
			virtual_machine.sleep_due_ms,
			"sleep_begin",
			virtual_machine.workload.identifier,
		)

	def _activity(self, virtual_machine: _VM) -> None:
		if virtual_machine.state == "asleep":
			host = self._hosts[virtual_machine.host_name]
			shortfall_mib = max(
				0,
				self._used(host).memory_mib
				+ virtual_machine.workload.request.memory_mib
				- host.host_type.resources.memory_mib,
			)
			if shortfall_mib:
				self._wake_shortfalls += 1
				self._peak_wake_shortfall_mib = max(self._peak_wake_shortfall_mib, shortfall_mib)
			virtual_machine.state = "waking"
			self._wakes += 1
			self._schedule(
				self._now_ms + round(self.scenario.wake_seconds * 1000),
				"wake",
				virtual_machine.workload.identifier,
			)
		elif virtual_machine.state in ("active", "saving"):
			virtual_machine.state = "active"
			self._sleep(virtual_machine)

	def _accrue(self, next_ms: int) -> None:
		elapsed_ms = next_ms - self._now_ms
		if elapsed_ms <= 0:
			return
		self._host_milliseconds += len(self._hosts) * elapsed_ms
		capacity = Resources(0, 0, 0)
		used = Resources(0, 0, 0)
		for host in self._hosts.values():
			if not host.ready:
				continue
			host_used = self._used(host)
			capacity = Resources(
				capacity.cpu_millicores + host.host_type.resources.cpu_millicores,
				capacity.memory_mib + host.host_type.resources.memory_mib,
				capacity.storage_mib + host.host_type.resources.storage_mib,
			)
			used = Resources(
				used.cpu_millicores + host_used.cpu_millicores,
				used.memory_mib + host_used.memory_mib,
				used.storage_mib + host_used.storage_mib,
			)
		self._ready_capacity_area = Resources(
			self._ready_capacity_area.cpu_millicores + capacity.cpu_millicores * elapsed_ms,
			self._ready_capacity_area.memory_mib + capacity.memory_mib * elapsed_ms,
			self._ready_capacity_area.storage_mib + capacity.storage_mib * elapsed_ms,
		)
		self._ready_used_area = Resources(
			self._ready_used_area.cpu_millicores + used.cpu_millicores * elapsed_ms,
			self._ready_used_area.memory_mib + used.memory_mib * elapsed_ms,
			self._ready_used_area.storage_mib + used.storage_mib * elapsed_ms,
		)

	def run(self, name: str, strategy: Callable[[SimulatedPlacementAPI], None]) -> Result:
		while self._events:
			at_ms, _, kind, identifier = heapq.heappop(self._events)
			self._accrue(at_ms)
			self._now_ms = at_ms
			if kind == "host_ready":
				self._hosts[identifier].ready = True
				continue
			virtual_machine = self._virtual_machines[identifier]
			if kind in ("arrive", "retry"):
				self._place(virtual_machine, strategy)
			elif kind == "depart":
				if virtual_machine.state == "waiting":
					self._expired_unplaced += 1
				if virtual_machine.host_name is not None:
					self._hosts[virtual_machine.host_name].virtual_machines.remove(identifier)
				virtual_machine.state = "departed"
			elif kind == "activity":
				self._activity(virtual_machine)
			elif kind == "boot" and virtual_machine.state == "booting":
				virtual_machine.state = "active"
				if virtual_machine.workload.request.is_sleepy:
					self._sleep(virtual_machine)
			elif (
				kind == "sleep_begin"
				and virtual_machine.state == "active"
				and virtual_machine.sleep_due_ms == self._now_ms
			):
				virtual_machine.state = "saving"
				virtual_machine.save_due_ms = self._now_ms + round(self.scenario.save_seconds * 1000)
				self._schedule(virtual_machine.save_due_ms, "sleep", identifier)
			elif (
				kind == "sleep"
				and virtual_machine.state == "saving"
				and virtual_machine.save_due_ms == self._now_ms
			):
				virtual_machine.state = "asleep"
				self._sleeps += 1
			elif kind == "wake" and virtual_machine.state == "waking":
				virtual_machine.state = "active"
				self._sleep(virtual_machine)
		self._accrue(self._horizon_ms)
		pending = sum(vm.state == "waiting" for vm in self._virtual_machines.values())
		return Result(
			strategy=name,
			requested=len(self._virtual_machines),
			placed=len(self._wait_ms),
			delayed_placements=sum(wait_ms > 0 for wait_ms in self._wait_ms),
			rejected=self._rejected,
			expired_unplaced=self._expired_unplaced,
			pending_at_end=pending,
			wait_p50_seconds=_percentile(self._wait_ms, 50) / 1000,
			wait_p95_seconds=_percentile(self._wait_ms, 95) / 1000,
			wait_max_seconds=max(self._wait_ms, default=0) / 1000,
			hosts_at_end=len(self._hosts),
			new_hosts=len(self._hosts) - len(self.scenario.initial_hosts),
			host_hours=self._host_milliseconds / 3_600_000,
			ready_memory_utilization=_ratio(
				self._ready_used_area.memory_mib, self._ready_capacity_area.memory_mib
			),
			ready_storage_utilization=_ratio(
				self._ready_used_area.storage_mib, self._ready_capacity_area.storage_mib
			),
			ready_cpu_allocation=_ratio(
				self._ready_used_area.cpu_millicores, self._ready_capacity_area.cpu_millicores
			),
			sleeps=self._sleeps,
			wakes=self._wakes,
			wake_memory_shortfalls=self._wake_shortfalls,
			peak_wake_shortfall_mib=self._peak_wake_shortfall_mib,
		)


def _percentile(values: list[int], percent: int) -> int:
	if not values:
		return 0
	return sorted(values)[(len(values) * percent + 99) // 100 - 1]


def _ratio(used: int, capacity: int) -> float:
	return used / capacity if capacity else 0.0
