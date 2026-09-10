from __future__ import annotations

import frappe
from frappe.model.document import Document


class VirtualMachineMigration(Document):
	"""One move of a virtual machine from a source to a target Metal Server."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		abort_requested: DF.Check
		progress: DF.Code | None
		source_server: DF.Link
		started_at: DF.Datetime | None
		status: DF.Literal["running", "ready", "completed", "failed", "aborted"]
		target_server: DF.Link
		virtual_machine: DF.Link
	# end: auto-generated types

	@frappe.whitelist(methods=["POST"])
	def abort(self) -> None:
		"""Record an abort request and queue the worker to continue it."""
		frappe.get_doc("Virtual Machine", self.virtual_machine).check_permission("write")
		self.db_set("abort_requested", 1)
		frappe.db.commit()  # nosemgrep
		from atlas.vm.core.vm_migration import enqueue_migration

		enqueue_migration(self.name)
