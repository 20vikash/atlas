# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

from frappe.model.document import Document


class MetalServerSize(Document):
	"""One provider machine type in the catalog."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		architecture: DF.Literal["amd64", "arm64"]
		cpu_count: DF.Int
		disk_gib: DF.Int
		enabled: DF.Check
		hourly_pricing_usd_cents: DF.Int
		memory_mib: DF.Int
		monthly_pricing_usd_cents: DF.Int
		provider_metadata: DF.Code | None
	# end: auto-generated types
