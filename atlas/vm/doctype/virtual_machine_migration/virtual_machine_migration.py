from __future__ import annotations

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
