from __future__ import annotations

import ipaddress
from typing import TYPE_CHECKING, cast

import frappe
from frappe import _
from frappe.model.document import Document

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings


def get_virtual_machine_mesh_address(virtual_machine: Document | frappe._dict) -> str:
	"""Return the mesh address from stable Atlas request metadata."""
	settings = cast("AtlasSettings", frappe.get_single("Atlas Settings"))
	region_id = settings.region_id
	if not 0 <= region_id <= 0xFFFF:
		frappe.throw(_("Atlas Settings region ID must be a 16-bit unsigned integer."))

	virtual_machine_name = cast(str, virtual_machine.name)
	virtual_machine_number = int(virtual_machine_name.rsplit("-", 1)[-1])
	if virtual_machine_number > 0xFFFFFFFF:
		frappe.throw(
			_("Virtual Machine number {0} is too large for a mesh address.").format(virtual_machine_number)
		)

	address = (0xFDAA << 112) | (region_id << 96) | (virtual_machine.tenant_id << 64) | virtual_machine_number
	return str(ipaddress.IPv6Address(address))
