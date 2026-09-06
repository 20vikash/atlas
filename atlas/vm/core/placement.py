from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import datetime, timedelta
from typing import TYPE_CHECKING, cast

import frappe
from frappe import _
from frappe.utils import now_datetime

from atlas.vm.core.models import VirtualMachineCreateRequest

if TYPE_CHECKING:
	from atlas.server.doctype.server.server import Server

CAPACITY_MAXIMUM_AGE = timedelta(minutes=2)


@dataclass(frozen=True, slots=True)
class PlacementCapacity:
	"""Store available host capacity at one sample time."""

	server: str
	architecture: str
	sample_created_at: datetime
	available_cpu_count: int
	available_memory_mib: int
	available_storage_mib: int

	def reserve(self, virtual_cpu_count: int, memory_mib: int, disk_mib: int) -> PlacementCapacity:
		"""Subtract one local reservation without producing negative capacity."""
		return replace(
			self,
			available_cpu_count=max(self.available_cpu_count - virtual_cpu_count, 0),
			available_memory_mib=max(self.available_memory_mib - memory_mib, 0),
			available_storage_mib=max(self.available_storage_mib - disk_mib, 0),
		)

	def can_host(self, request: VirtualMachineCreateRequest, architecture: str) -> bool:
		"""Return whether this capacity can hold the request."""
		return (
			self.architecture == architecture
			and self.available_cpu_count >= request.virtual_cpu_count
			and self.available_memory_mib >= request.memory_mib
			and self.available_storage_mib >= request.disk_mib
		)

	@property
	def rank(self) -> tuple[int, int, int, str]:
		"""Return the stable best-capacity ordering key."""
		return (
			self.available_memory_mib,
			self.available_cpu_count,
			self.available_storage_mib,
			self.server,
		)


class PlacementService:
	"""Select and lock one Server with current effective capacity."""

	def select_server(self, request: VirtualMachineCreateRequest, architecture: str) -> Server:
		"""Return one locked Server that can hold the request."""
		servers = self.get_ready_servers()
		if not servers:
			frappe.throw(_("No running Server is ready for Virtual Machines."))

		architecture_by_server = {server.name: server.architecture for server in servers}
		capacities = self.get_latest_capacities(architecture_by_server)
		if not capacities:
			frappe.throw(_("No current Server capacity sample is available. Check Server synchronization."))
		candidates = sorted(
			(self.subtract_local_reservations(capacity) for capacity in capacities.values()),
			key=lambda capacity: capacity.rank,
			reverse=True,
		)
		for capacity in candidates:
			if not capacity.can_host(request, architecture):
				continue

			server = self.lock_server(capacity.server)
			if not self.is_ready_server(server, architecture):
				continue

			current_capacity = self.get_latest_capacities({capacity.server: architecture}).get(
				capacity.server
			)
			if current_capacity is None:
				continue
			current_capacity = self.subtract_local_reservations(current_capacity)
			if current_capacity.can_host(request, architecture):
				return server

		frappe.throw(_("No Server has current capacity for this Virtual Machine."))
		raise AssertionError

	def get_ready_servers(self) -> list[frappe._dict]:
		"""Return Servers that are ready to host virtual machines."""
		return frappe.get_all(
			"Server",
			filters={"status": "Running", "is_provisioning_completed": 1},
			fields=["name", "architecture"],
		)

	def get_latest_capacities(self, architecture_by_server: dict[str, str]) -> dict[str, PlacementCapacity]:
		"""Return the latest fresh capacity for each supplied Server."""
		if not architecture_by_server:
			return {}

		freshness_cutoff = now_datetime() - CAPACITY_MAXIMUM_AGE
		usage_rows = frappe.get_all(
			"Server Usage",
			filters={
				"server": ["in", list(architecture_by_server)],
				"creation": [">=", freshness_cutoff],
			},
			fields=[
				"server",
				"creation",
				"available_cpu_count",
				"available_memory_mib",
				"available_storage_mib",
			],
			order_by="creation desc",
		)

		capacities: dict[str, PlacementCapacity] = {}
		for usage_row in usage_rows:
			if usage_row.server in capacities:
				continue
			capacities[usage_row.server] = PlacementCapacity(
				server=usage_row.server,
				architecture=architecture_by_server[usage_row.server],
				sample_created_at=usage_row.creation,
				available_cpu_count=usage_row.available_cpu_count,
				available_memory_mib=usage_row.available_memory_mib,
				available_storage_mib=usage_row.available_storage_mib,
			)
		return capacities

	def subtract_local_reservations(self, capacity: PlacementCapacity) -> PlacementCapacity:
		"""Subtract requests that Metal cannot include in this capacity sample."""
		reservations = frappe.get_all(
			"Virtual Machine",
			filters={"server": capacity.server},
			or_filters={
				"creation": [">", capacity.sample_created_at],
				"is_draft": 1,
			},
			fields=["vcpus", "memory_mib", "disk_mib"],
		)
		for reservation in reservations:
			capacity = capacity.reserve(
				virtual_cpu_count=reservation.vcpus,
				memory_mib=reservation.memory_mib,
				disk_mib=reservation.disk_mib,
			)
		return capacity

	def lock_server(self, server_name: str) -> Server:
		"""Lock one Server row for the current database transaction."""
		return cast("Server", frappe.get_doc("Server", server_name, for_update=True))

	@staticmethod
	def is_ready_server(server: Server, architecture: str) -> bool:
		"""Return whether the locked Server is still an eligible host."""
		return (
			server.status == "Running"
			and server.is_provisioning_completed
			and server.architecture == architecture
		)
