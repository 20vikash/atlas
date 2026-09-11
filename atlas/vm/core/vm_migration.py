from __future__ import annotations

import time
from datetime import timedelta
from typing import TYPE_CHECKING, Any, cast

import frappe
from frappe import _
from frappe.utils import get_datetime, now_datetime

from atlas.atlas.core.background_jobs import run_as_admin
from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.metal_client import MetalClient, MetalClientError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement import PlacementService

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer
	from atlas.vm.doctype.virtual_machine.virtual_machine import VirtualMachine
	from atlas.vm.doctype.virtual_machine_migration.virtual_machine_migration import (
		VirtualMachineMigration,
	)

MIGRATABLE_STATES = frozenset({"running", "stopped", "paused"})
COPY_POLL_SECONDS = 5
TRANSITION_POLL_SECONDS = 2
TRANSITION_PHASES = frozenset({"stopping", "starting"})
MAXIMUM_RUN_DURATION = timedelta(minutes=30)
MIGRATION_JOB_TIMEOUT_SECONDS = int(MAXIMUM_RUN_DURATION.total_seconds()) + 120
TARGET_VISIBILITY_TIMEOUT = timedelta(minutes=10)


class MigrationService:
	"""Own Atlas orchestration for one virtual machine migration."""

	def __init__(self, migration: VirtualMachineMigration) -> None:
		self.migration = migration

	@classmethod
	def create(cls, virtual_machine: VirtualMachine, target_server: str | None = None) -> str:
		"""Lock the VM, reserve a target, and open one migration. Return its ID.

		With no target_server, Atlas selects a host. With one, it uses that host
		after it confirms the host is eligible and has capacity.
		"""
		locked = cast(
			"VirtualMachine",
			frappe.get_doc("Virtual Machine", virtual_machine.name, for_update=True),
		)
		cls.validate_source(locked)

		shape = cls.get_shape(locked)
		architecture = cls.get_architecture(locked)
		placement = PlacementService()
		if target_server:
			if target_server == locked.server:
				frappe.throw(
					_("Choose a target host other than {0}.").format(locked.server),
					exc=AtlasUserError,
				)
			target = placement.select_target_server(shape, architecture, target_server)
		else:
			target = placement.select_server(shape, architecture, exclude_servers={locked.server})
		migration = frappe.get_doc(
			{
				"doctype": "Virtual Machine Migration",
				"virtual_machine": locked.name,
				"source_server": locked.server,
				"target_server": target.name,
				"status": "running",
				"started_at": now_datetime(),
				"progress": "{}",
			}
		).insert(ignore_permissions=True)

		locked.db_set("active_migration", migration.name)
		frappe.db.commit()  # nosemgrep
		enqueue_migration(cast(str, migration.name))
		return cast(str, migration.name)

	@staticmethod
	def validate_source(virtual_machine: VirtualMachine) -> None:
		"""Reject a VM that cannot start a migration."""
		if virtual_machine.active_migration:
			frappe.throw(
				_("Virtual Machine {0} is already migrating.").format(virtual_machine.name),
				exc=AtlasUserError,
			)
		if virtual_machine.is_draft or virtual_machine.is_terminating:
			frappe.throw(
				_("Virtual Machine {0} is not ready to migrate.").format(virtual_machine.name),
				exc=AtlasUserError,
			)
		if virtual_machine.current_state not in MIGRATABLE_STATES:
			frappe.throw(
				_("Virtual Machine {0} must be running, stopped, or paused to migrate.").format(
					virtual_machine.name
				),
				exc=AtlasUserError,
			)

	@staticmethod
	def get_shape(virtual_machine: VirtualMachine) -> VirtualMachineCreateRequest:
		"""Return the placement shape for the migrating VM."""
		return VirtualMachineCreateRequest(
			virtual_machine_image=virtual_machine.virtual_machine_image,
			virtual_cpu_count=virtual_machine.vcpus,
			memory_mib=virtual_machine.memory_mib,
			disk_mib=virtual_machine.disk_mib,
			tenant_id=virtual_machine.tenant_id,
		)

	@staticmethod
	def get_architecture(virtual_machine: VirtualMachine) -> str:
		"""Return the image architecture for placement."""
		return cast(
			str,
			frappe.db.get_value("Virtual Machine Image", virtual_machine.virtual_machine_image, "platform"),
		)

	def run(self) -> None:
		"""Send the request, then poll the target until the migration settles.

		The request repeats on every non-abort run, so a recovery run re-sends the
		target-pull request to a target that lost it.
		"""
		if not self.migration.abort_requested:
			self.send_request()

		deadline = now_datetime() + MAXIMUM_RUN_DURATION
		while now_datetime() < deadline:
			status = self.poll()
			if self.advance(status):
				return
			time.sleep(self.poll_interval(status))

	def advance(self, status: dict[str, Any]) -> bool:
		"""Apply one Metal status. Return True when the migration is settled."""
		state = status.get("status")
		if state == "completed":
			self.settle("completed")
			return True
		if state == "aborted":
			self.settle("aborted")
			return True
		if state == "failed":
			self.mark_failed()
			self.request_abort()
			return False
		if state == "missing":
			return self.handle_missing_target()
		if self.migration.abort_requested:
			self.request_abort()
			return False
		if state == "ready":
			self.commit_target()
			self.finish()
		return False

	def handle_missing_target(self) -> bool:
		"""Retry an invisible target, or expire it after the visibility timeout."""
		if self.is_expired:
			self.expire()
			return True
		self.send_request()
		return False

	def expire(self) -> None:
		"""Abort any target remnant, fail the migration, and release the VM lock."""
		try:
			self.target_client.abort_migration(cast(str, self.migration.name))
		except MetalClientError as error:
			if not error.is_not_found:
				self.record_error(error)
		self.settle("failed")

	def send_request(self) -> None:
		"""Send the target-pull request. Safe to repeat."""
		source = MetalClient.get_api_url(self.source_server)
		self.target_client.put_migration(
			cast(str, self.migration.name), self.migration.virtual_machine, source
		)

	def poll(self) -> dict[str, Any]:
		"""Read the target migration status and store it as progress.

		The write commits, so an open form and a recovery run see live progress
		during the copy, not only the final result. It merges into the stored
		progress, so the compact terminal record does not erase the copy history
		such as the per-interval throughput.
		"""
		try:
			status = self.target_client.get_migration(cast(str, self.migration.name))
		except MetalClientError as error:
			if error.is_not_found:
				return {"status": "missing"}
			raise
		stored = frappe.parse_json(self.migration.progress or "{}")
		if not isinstance(stored, dict):
			stored = {}
		self.migration.db_set("progress", frappe.as_json({**stored, **status}), commit=True)
		return status

	@staticmethod
	def poll_interval(status: dict[str, Any]) -> int:
		"""Return the wait before the next poll. Transitions poll more often than a copy."""
		return TRANSITION_POLL_SECONDS if status.get("phase") in TRANSITION_PHASES else COPY_POLL_SECONDS

	def commit_target(self) -> None:
		"""Point the VM at the target and mark the migration ready in one transaction."""
		virtual_machine = frappe.get_doc("Virtual Machine", self.migration.virtual_machine, for_update=True)
		migration = cast(
			"VirtualMachineMigration",
			frappe.get_doc("Virtual Machine Migration", self.migration.name, for_update=True),
		)
		if virtual_machine.server != migration.target_server:
			virtual_machine.db_set("server", migration.target_server)
		if migration.status != "ready":
			migration.db_set("status", "ready")
		frappe.db.commit()  # nosemgrep
		self.migration = migration

	def finish(self) -> None:
		"""Tell the target that Atlas committed the VM. Safe to repeat."""
		self.target_client.finish_migration(cast(str, self.migration.name))

	def request_abort(self) -> None:
		"""Record the abort intent, then ask the target to abort. Safe to repeat."""
		if not self.migration.abort_requested:
			self.migration.db_set("abort_requested", 1)
			frappe.db.commit()  # nosemgrep
		try:
			self.target_client.abort_migration(cast(str, self.migration.name))
		except MetalClientError as error:
			self.record_error(error)

	def mark_failed(self) -> None:
		"""Record that Metal reported a failed migration."""
		if self.migration.status != "failed":
			self.migration.db_set("status", "failed")

	def settle(self, status: str) -> None:
		"""Release the VM lock and record the final migration status."""
		frappe.db.set_value("Virtual Machine", self.migration.virtual_machine, "active_migration", None)
		self.migration.db_set("status", status)
		frappe.db.commit()  # nosemgrep

	def record_error(self, error: MetalClientError) -> None:
		"""Store one migration error without releasing the VM lock."""
		progress = frappe.parse_json(self.migration.progress or "{}")
		if not isinstance(progress, dict):
			progress = {}
		progress["error"] = str(error)
		self.migration.db_set("progress", frappe.as_json(progress))
		frappe.db.commit()  # nosemgrep

	@property
	def is_expired(self) -> bool:
		"""Report whether the target stayed invisible past the visibility timeout."""
		return now_datetime() > get_datetime(self.migration.started_at) + TARGET_VISIBILITY_TIMEOUT

	@property
	def is_target_committed(self) -> bool:
		"""Report whether the VM already points at the target host."""
		return (
			frappe.db.get_value("Virtual Machine", self.migration.virtual_machine, "server")
			== self.migration.target_server
		)

	@property
	def target_client(self) -> MetalClient:
		"""Return a Metal client for the target host."""
		return MetalClient(cast("MetalServer", frappe.get_doc("Metal Server", self.migration.target_server)))

	@property
	def source_server(self) -> MetalServer:
		"""Return the source Metal Server record."""
		return cast("MetalServer", frappe.get_doc("Metal Server", self.migration.source_server))


def enqueue_migration(migration_name: str) -> None:
	"""Queue one deduplicated migration worker."""
	frappe.enqueue(
		"atlas.vm.core.vm_migration.run_migration",
		queue="long",
		timeout=MIGRATION_JOB_TIMEOUT_SECONDS,
		migration_name=migration_name,
		job_id=f"atlas||vm-migration||{migration_name}",
		deduplicate=True,
		enqueue_after_commit=True,
	)


@run_as_admin
def run_migration(migration_name: str) -> None:
	"""Advance one migration to completion, or record why it stopped."""
	migration = cast("VirtualMachineMigration", frappe.get_doc("Virtual Machine Migration", migration_name))
	service = MigrationService(migration)
	try:
		service.run()
	except MetalClientError as error:
		service.record_error(error)
		frappe.log_error(
			title=f"Virtual Machine migration {migration_name} failed",
			message=frappe.get_traceback(),
		)


@run_as_admin
def reconcile_migrations() -> None:
	"""Requeue every migration that still holds a VM action lock."""
	names = frappe.get_all(
		"Virtual Machine", filters={"active_migration": ["is", "set"]}, pluck="active_migration"
	)
	for name in names:
		enqueue_migration(name)
