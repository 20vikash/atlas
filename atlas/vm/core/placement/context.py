from __future__ import annotations

from collections import Counter
from collections.abc import Iterable
from datetime import timedelta
from typing import TYPE_CHECKING, cast

import frappe
from frappe import _
from frappe.utils import now_datetime

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.models import FleetUsage, HostUsage, PlacementDemand, Resources

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer

CAPACITY_MAXIMUM_AGE = timedelta(minutes=2)
PLACEMENT_RATE_WINDOW = timedelta(minutes=5)


class CapacityPending(AtlasUserError):
	"""A host is provisioning for this VM shape."""

	http_status_code = 503


class PlacementContext:
	"""Give one strategy a fleet snapshot and a checked host selection."""

	def __init__(
		self,
		request: VirtualMachineCreateRequest,
		architecture: str,
		sleepy_vm_overcommit_factor: float,
		exclude_servers: set[str] | None = None,
	) -> None:
		self.request = PlacementDemand(
			cpu_millicores=request.cpu_millicores,
			memory_mib=request.memory_mib,
			disk_mib=request.disk_mib,
			architecture=architecture,
			tenant_id=request.tenant_id,
			is_sleepy=request.sleep_after_idle_seconds > 0,
		)
		self.action = "migration" if exclude_servers else "create"
		self.current_host_name: str | None = None
		self.sleepy_vm_overcommit_factor = sleepy_vm_overcommit_factor
		self._excluded_servers = frozenset(exclude_servers or ())
		self._created_at = now_datetime()
		self._placement_counts: Counter[str] | None = None
		self._selected_server: MetalServer | None = None
		self._pending_hosts: set[str] = set()
		self._host_intents: list[tuple[str | None, int, bool]] = []
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

	def spawn_host(self, host_type: str | None = None, *, count: int = 1, is_sleepy: bool = False) -> None:
		"""Request suitable hosts for this placement."""
		if isinstance(count, bool) or not isinstance(count, int) or count < 1:
			raise ValueError("count must be a positive integer")
		self._host_intents.append((host_type, count, is_sleepy))

	def _ensure_pending_hosts(self, host_type: str | None, count: int, is_sleepy: bool) -> tuple[str, ...]:
		"""Insert only the missing host intents under a settings lock."""
		stale_hosts = sorted(
			name
			for name, (architecture, sleepy) in self._stale_ready_servers.items()
			if architecture == self.request.architecture and sleepy == is_sleepy
		)
		if stale_hosts:
			frappe.throw(
				_("Metal Server {0} has no current capacity sample. Check Server synchronization.").format(
					", ".join(stale_hosts)
				),
				exc=AtlasUserError,
			)

		settings = frappe.get_doc("Atlas Settings", for_update=True)
		selected_type = host_type or settings.new_host_type
		if not selected_type:
			frappe.throw(
				_("Set New Host Type in Atlas Settings before expanding capacity."), exc=AtlasUserError
			)

		size = frappe.get_doc("Metal Server Size", selected_type)
		image = frappe.get_doc("Metal Server Image", f"{settings.server_provider}/Ubuntu_26.04")
		self._validate_host_catalog(size, image, settings.server_provider)

		# A locking read sees host intents committed while this request waited for Settings.
		servers = frappe.db.sql(
			"""select name, status, architecture, is_sleepy from `tabMetal Server`
			where server_size = %(server_size)s and status in ('Pending', 'Installing', 'Failed')
			order by creation desc for update""",
			{"server_size": selected_type},
			as_dict=True,
		)
		matching = [
			server
			for server in servers
			if server.architecture == self.request.architecture and bool(server.is_sleepy) == is_sleepy
		]
		pending = [server for server in matching if server.status in ("Pending", "Installing")]
		names = [row.name for row in pending]
		if len(names) >= count:
			return tuple(names)

		if not names and matching and matching[0].status == "Failed":
			frappe.throw(
				_(
					"Metal Server {0} failed to provision. Retry or remove it before expanding capacity."
				).format(matching[0].name),
				exc=AtlasUserError,
			)

		from atlas.metal_server.doctype.metal_server.metal_server import MetalServer

		for _host_index in range(count - len(names)):
			server = MetalServer.provision(size=selected_type, is_sleepy=is_sleepy)
			names.append(server.name)
		return tuple(names)

	def _validate_host_catalog(self, size: frappe._dict, image: frappe._dict, provider: str) -> None:
		"""Reject a host catalog entry that cannot hold this VM."""
		if not size.enabled or size.provider_type != provider or size.architecture not in ("amd64", "arm64"):
			frappe.throw(
				_("Metal Server Size {0} has invalid provider or architecture data.").format(size.name),
				exc=AtlasUserError,
			)
		if size.architecture != self.request.architecture:
			frappe.throw(
				_("Metal Server Size {0} does not support {1} VMs.").format(
					size.name, self.request.architecture
				),
				exc=AtlasUserError,
			)
		if any(
			not isinstance(value, int) or value <= 0
			for value in (size.cpu_count, size.memory_mib, size.disk_gib)
		):
			frappe.throw(
				_("Metal Server Size {0} has invalid capacity data.").format(size.name), exc=AtlasUserError
			)
		if size.memory_mib < self.request.memory_mib or size.disk_gib * 1024 < self.request.disk_mib:
			frappe.throw(
				_("Metal Server Size {0} cannot hold this VM shape.").format(size.name), exc=AtlasUserError
			)
		if not image.enabled or image.provider_type != provider:
			frappe.throw(
				_("Metal Server Image {0} is disabled or belongs to another provider.").format(image.name),
				exc=AtlasUserError,
			)
		for document in (size, image):
			try:
				metadata = frappe.parse_json(document.provider_metadata or "{}")
			except (TypeError, ValueError) as error:
				raise AtlasUserError(
					_("{0} {1} has invalid provider metadata.").format(document.doctype, document.name)
				) from error
			if not isinstance(metadata, dict) or not metadata:
				frappe.throw(
					_("{0} {1} has invalid provider metadata.").format(document.doctype, document.name),
					exc=AtlasUserError,
				)
			if document is image and (not isinstance(metadata.get("id"), str) or not metadata["id"]):
				frappe.throw(
					_("Metal Server Image {0} has no provider image ID.").format(image.name),
					exc=AtlasUserError,
				)

	def finish(self) -> MetalServer:
		"""Return the selection or save only host intents for a retry."""
		if self._host_intents and self._selected_server is None:
			frappe.db.rollback()
		for host_type, count, is_sleepy in self._host_intents:
			self._pending_hosts.update(self._ensure_pending_hosts(host_type, count, is_sleepy))

		if self._selected_server is not None:
			return self._selected_server
		if self._pending_hosts:
			frappe.db.commit()  # nosemgrep
			raise CapacityPending(
				_(
					"Capacity is pending on Metal Server {0}. Retry after it is ready and synchronized."
				).format(", ".join(sorted(self._pending_hosts)))
			)
		frappe.throw(_("No Metal Server has current capacity for this Virtual Machine."), exc=AtlasUserError)

	def _load_usage(self) -> FleetUsage:
		servers = frappe.get_all(
			"Metal Server",
			filters={"status": "Running", "is_provisioning_completed": 1},
			fields=["name", "architecture", "is_sleepy"],
			order_by="name",
		)
		server_names = [server.name for server in servers]
		self._ready_server_names = frozenset(server_names)
		samples = self._latest_samples(server_names) if server_names else {}
		self._stale_ready_servers = {
			server.name: (server.architecture, bool(server.is_sleepy))
			for server in servers
			if server.name not in samples
		}
		if servers and not samples:
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
