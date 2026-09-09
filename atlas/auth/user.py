from __future__ import annotations

import frappe

CENTRAL_ADMIN_USER = "central-admin@atlas.local"
ATLAS_ADMIN_ROLE = "Atlas Admin"


def create_central_admin_user() -> None:
	"""Create the user that a validated central token becomes."""
	if frappe.db.exists("User", CENTRAL_ADMIN_USER):
		return

	frappe.get_doc(
		{
			"doctype": "User",
			"name": CENTRAL_ADMIN_USER,
			"email": CENTRAL_ADMIN_USER,
			"first_name": "Central Admin",
			"user_type": "Website User",
			"enabled": 1,
			"send_welcome_email": 0,
			"roles": [{"role": ATLAS_ADMIN_ROLE}],
		}
	).insert(ignore_permissions=True)
