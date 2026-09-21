from frappe.model.document import Document


class VirtualMachineMigrationTransfer(Document):
	"""One disk transfer of a virtual machine migration."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		completed: DF.Check
		duration_seconds: DF.Duration | None
		finished_at: DF.Datetime | None
		parent: DF.Data
		parentfield: DF.Data
		parenttype: DF.Data
		started_at: DF.Datetime | None
		throughput_mibps: DF.Int
		total_mib: DF.Int
		transferred_mib: DF.Int
	# end: auto-generated types
