from collections.abc import Mapping

import frappe

from atlas.atlas.core.server_providers.scaleway.catalog import ScalewayCatalog


def execute() -> None:
	for size in frappe.get_all(
		"Metal Server Size", filters={"provider_type": "Scaleway"}, fields=["name", "provider_metadata"]
	):
		metadata = frappe.parse_json(size.provider_metadata or "{}")
		if not isinstance(metadata, Mapping):
			raise ValueError(f"Metal Server Size {size.name} has invalid Scaleway offer metadata")
		offer = metadata.get("hourly") or metadata.get("monthly")
		if not isinstance(offer, Mapping):
			raise ValueError(f"Metal Server Size {size.name} has no Scaleway offer metadata")

		architecture = ScalewayCatalog.offer_architecture(offer)
		frappe.db.set_value(
			"Metal Server Size", size.name, "architecture", architecture, update_modified=False
		)
