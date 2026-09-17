from __future__ import annotations

from collections import Counter
from collections.abc import Iterable
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import TYPE_CHECKING, cast

import frappe
from frappe import _
from frappe.utils import now_datetime

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer

CAPACITY_MAXIMUM_AGE = timedelta(minutes=2)
PLACEMENT_RATE_WINDOW = timedelta(minutes=5)


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
class PlacementRequest:
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


class PlacementAPI:
	"""Give one strategy a fleet snapshot and a checked host selection."""

	def __init__(
		self,
		request: VirtualMachineCreateRequest,
		architecture: str,
		sleepy_vm_overcommit_factor: float,
		exclude_servers: set[str] | None = None,
	) -> None:
		self.request = PlacementRequest(
			cpu_millicores=request.cpu_millicores,
			memory_mib=request.memory_mib,
			disk_mib=request.disk_mib,
			architecture=architecture,
			tenant_id=request.tenant_id,
			is_sleepy=request.sleep_after_idle_seconds > 0,
		)
		self.sleepy_vm_overcommit_factor = sleepy_vm_overcommit_factor
		self._excluded_servers = frozenset(exclude_servers or ())
		self._created_at = now_datetime()
		self._placement_counts: Counter[str] | None = None
		self._selected_server: MetalServer | None = None
		self.usage = self._load_usage()

	def placement_rate(self, host_name: str | None = None) -> float:
		"""Return VM creations per minute over five minutes, including drafts."""
		if self._placement_counts is None:
			rows = frappe.get_all(
				"Virtual Machine",
				filters={"creation": [">=", self._created_at - PLACEMENT_RATE_WINDOW]},
				fields=["server"],
			)
			self._placement_counts = Counter(row.server for row in rows)

		count = self._placement_counts[host_name] if host_name is not None else self._placement_counts.total()
		return count / (PLACEMENT_RATE_WINDOW.total_seconds() / 60)

	def select(self, host_name: str) -> bool:
		"""Lock and recheck a host. Return false if its state changed or capacity was spent."""
		if self._selected_server is not None:
			raise RuntimeError("A placement host is already selected.")

		if host_name in self._excluded_servers:
			return False

		host = next((host for host in self.usage.hosts if host.name == host_name), None)
		if host is None:
			if host_name in self._ready_server_names:
				frappe.throw(
					_(
						"No current capacity sample for Metal Server {0}. Check Server synchronization."
					).format(host_name),
					exc=AtlasUserError,
				)
			return False

		server = cast("MetalServer", frappe.get_doc("Metal Server", host_name, for_update=True))
		if (
			server.status != "Running"
			or not server.is_provisioning_completed
			or server.architecture != self.request.architecture
			or bool(server.is_sleepy) != host.is_sleepy
		):
			return False

		sample = self._latest_samples([host_name]).get(host_name)
		if sample is None:
			frappe.throw(
				_("No current capacity sample for Metal Server {0}. Check Server synchronization.").format(
					host_name
				),
				exc=AtlasUserError,
			)

		virtual_machines = self._virtual_machines([host_name])[host_name]
		migrations = self._migration_reservations([host_name])[host_name]
		current = self._host_usage(server, sample, virtual_machines, migrations)
		if (
			current.free.memory_mib < self.request.memory_mib
			or current.free.storage_mib < self.request.disk_mib
		):
			return False

		self._selected_server = server
		return True

	def _load_usage(self) -> FleetUsage:
		servers = frappe.get_all(
			"Metal Server",
			filters={"status": "Running", "is_provisioning_completed": 1},
			fields=["name", "architecture", "is_sleepy"],
			order_by="name",
		)
		if not servers:
			frappe.throw(_("No running Metal Server is ready for Virtual Machines."), exc=AtlasUserError)

		server_names = [server.name for server in servers]
		self._ready_server_names = frozenset(server_names)
		samples = self._latest_samples(server_names)
		if not samples:
			frappe.throw(
				_("No current Metal Server capacity sample is available. Check Server synchronization."),
				exc=AtlasUserError,
			)

		virtual_machines = self._virtual_machines(list(samples))
		migrations = self._migration_reservations(list(samples))
		hosts = tuple(
			self._host_usage(
				server, samples[server.name], virtual_machines[server.name], migrations[server.name]
			)
			for server in servers
			if server.name in samples
		)
		return FleetUsage(
			hosts=hosts,
			total=self._sum_resources(host.total for host in hosts),
			free=self._sum_resources(host.free for host in hosts),
			tenant_vm_count=sum(host.tenant_vm_count for host in hosts),
			sleepy_reserved_memory_mib=sum(host.sleepy_reserved_memory_mib for host in hosts),
		)

	@staticmethod
	def _sum_resources(resources: Iterable[Resources]) -> Resources:
		items = tuple(resources)
		return Resources(
			cpu_millicores=sum(item.cpu_millicores for item in items),
			memory_mib=sum(item.memory_mib for item in items),
			storage_mib=sum(item.storage_mib for item in items),
		)

	@staticmethod
	def _latest_samples(server_names: list[str]) -> dict[str, frappe._dict]:
		rows = frappe.get_all(
			"Metal Server Usage",
			filters={
				"server": ["in", server_names],
				"creation": [">=", now_datetime() - CAPACITY_MAXIMUM_AGE],
			},
			fields=[
				"server",
				"creation",
				"total_cpu_millicores",
				"available_cpu_millicores",
				"total_memory_mib",
				"available_memory_mib",
				"total_storage_mib",
				"available_storage_mib",
			],
			order_by="creation desc",
		)
		samples: dict[str, frappe._dict] = {}
		for row in rows:
			samples.setdefault(row.server, row)
		return samples

	@staticmethod
	def _virtual_machines(server_names: list[str]) -> dict[str, list[frappe._dict]]:
		rows = frappe.get_all(
			"Virtual Machine",
			filters={"server": ["in", server_names]},
			fields=[
				"server",
				"tenant_id",
				"sleep_after_idle_seconds",
				"cpu_millicores",
				"memory_mib",
				"disk_mib",
				"creation",
				"is_draft",
			],
		)
		by_server: dict[str, list[frappe._dict]] = {name: [] for name in server_names}
		for row in rows:
			by_server[row.server].append(row)
		return by_server

	@staticmethod
	def _migration_reservations(server_names: list[str]) -> dict[str, list[frappe._dict]]:
		rows = frappe.get_all(
			"Virtual Machine Migration",
			filters={"target_server": ["in", server_names], "status": ["in", ["running", "ready"]]},
			fields=["target_server", "virtual_machine"],
		)
		names_by_target: dict[str, set[str]] = {name: set() for name in server_names}
		for row in rows:
			names_by_target[row.target_server].add(row.virtual_machine)

		virtual_machine_names = set().union(*names_by_target.values())
		if not virtual_machine_names:
			return {name: [] for name in server_names}

		shapes = frappe.get_all(
			"Virtual Machine",
			filters={"name": ["in", list(virtual_machine_names)]},
			fields=["name", "cpu_millicores", "memory_mib", "disk_mib"],
		)
		shapes_by_name = {shape.name: shape for shape in shapes}
		return {
			name: [shapes_by_name[vm_name] for vm_name in names if vm_name in shapes_by_name]
			for name, names in names_by_target.items()
		}

	def _host_usage(
		self,
		server: frappe._dict | MetalServer,
		sample: frappe._dict,
		virtual_machines: list[frappe._dict],
		migrations: list[frappe._dict],
	) -> HostUsage:
		free = Resources(
			cpu_millicores=sample.available_cpu_millicores,
			memory_mib=sample.available_memory_mib,
			storage_mib=sample.available_storage_mib,
		)
		for virtual_machine in virtual_machines:
			if virtual_machine.is_draft or virtual_machine.creation > sample.creation:
				free = free.reserve(
					virtual_machine.cpu_millicores,
					virtual_machine.memory_mib,
					virtual_machine.disk_mib,
				)
		for migration in migrations:
			free = free.reserve(migration.cpu_millicores, migration.memory_mib, migration.disk_mib)

		return HostUsage(
			name=server.name,
			architecture=server.architecture,
			is_sleepy=bool(server.is_sleepy),
			sample_created_at=sample.creation,
			total=Resources(
				cpu_millicores=sample.total_cpu_millicores,
				memory_mib=sample.total_memory_mib,
				storage_mib=sample.total_storage_mib,
			),
			free=free,
			tenant_vm_count=sum(vm.tenant_id == self.request.tenant_id for vm in virtual_machines),
			sleepy_reserved_memory_mib=sum(
				vm.memory_mib for vm in virtual_machines if vm.sleep_after_idle_seconds > 0
			),
		)
