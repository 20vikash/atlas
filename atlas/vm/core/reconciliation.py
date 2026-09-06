from __future__ import annotations

from collections.abc import Callable
from typing import TYPE_CHECKING, cast

import frappe

from atlas.vm.core.metal_client import MetalClientError
from atlas.vm.core.vm_service import VirtualMachineService

if TYPE_CHECKING:
	from atlas.vm.doctype.virtual_machine.virtual_machine import VirtualMachine


def reconcile_stale_draft(name: str) -> None:
	"""Finalize a draft that Metal holds, or delete one that Metal never received.

	A create can leave a draft whose outcome Atlas never saw. This runs on a
	schedule, so a lost response costs one interval.
	"""
	settle(name, "draft reconciliation", on_present=lambda machine: machine.db_set("is_draft", 0))


def reconcile_terminating(name: str) -> None:
	"""Delete a terminating virtual machine after Metal confirms its absence."""
	settle(name, "termination reconciliation", on_present=None)


def settle(
	name: str,
	description: str,
	on_present: Callable[[VirtualMachine], None] | None,
) -> None:
	"""Ask Metal whether one virtual machine exists and settle the Atlas record.

	Metal is the authority. A confirmed absence deletes the record. Any other
	failure is logged and left alone, because an unreachable host says nothing
	about whether the virtual machine exists.
	"""
	virtual_machine = cast("VirtualMachine", frappe.get_doc("Virtual Machine", name))

	try:
		VirtualMachineService(virtual_machine).metal_client.get_virtual_machine(name)
	except MetalClientError as error:
		if not error.is_not_found:
			frappe.log_error(
				message=frappe.get_traceback(),
				title=f"Virtual Machine {name} {description} failed",
			)
			return

		virtual_machine.flags.metal_absence_confirmed = True
		virtual_machine.delete(ignore_permissions=True)
		return

	if on_present:
		on_present(virtual_machine)
