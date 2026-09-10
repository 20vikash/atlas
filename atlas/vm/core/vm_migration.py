from __future__ import annotations

from typing import TYPE_CHECKING, cast

import frappe
from frappe import _
from frappe.utils import now_datetime

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement import PlacementService

if TYPE_CHECKING:
	from atlas.vm.doctype.virtual_machine.virtual_machine import VirtualMachine
	from atlas.vm.doctype.virtual_machine_migration.virtual_machine_migration import (
		VirtualMachineMigration,
	)

MIGRATABLE_STATES = frozenset({"running", "stopped", "paused"})


class MigrationService:
	"""Own Atlas orchestration for one virtual machine migration."""

	def __init__(self, migration: VirtualMachineMigration) -> None:
		self.migration = migration

	@classmethod
	def create(cls, virtual_machine: VirtualMachine) -> str:
		"""Lock the VM, reserve a target, and open one migration. Return its ID."""
		locked = cast(
			"VirtualMachine",
			frappe.get_doc("Virtual Machine", virtual_machine.name, for_update=True),
		)
		cls.validate_source(locked)

		target = PlacementService().select_server(
			cls.get_shape(locked),
			cls.get_architecture(locked),
			exclude_servers={locked.server},
		)
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
			frappe.db.get_value(
				"Virtual Machine Image", virtual_machine.virtual_machine_image, "platform"
			),
		)
