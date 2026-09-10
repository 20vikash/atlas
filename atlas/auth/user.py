from __future__ import annotations

import frappe

CENTRAL_ADMIN_USER = "central-admin@atlas.local"
ATLAS_ADMIN_ROLE = "Atlas Admin"


def create_central_admin_user() -> None:
	"""Create the user that a validated central token becomes."""
	if frappe.db.exists("User", CENTRAL_ADMIN_USER):
		return

	_insert_api_user(CENTRAL_ADMIN_USER, "Central Admin")


def ensure_tenant_user(tenant_id: int) -> str:
	"""Return the user of one tenant, and create it on first use."""
	name = f"tenant-{tenant_id}@atlas.local"
	if frappe.db.exists("User", name):
		return name

	try:
		_insert_api_user(name, f"Tenant {tenant_id}")
		# The authentication hook runs before any route work, so this commit holds only the new
		# user. Frappe rolls a read request back, and the user must outlive the request.
		frappe.db.commit()
	except frappe.DuplicateEntryError:
		frappe.db.rollback()

	return name


def _insert_api_user(name: str, first_name: str) -> None:
	frappe.get_doc(
		{
			"doctype": "User",
			"name": name,
			"email": name,
			"first_name": first_name,
			"user_type": "Website User",
			"enabled": 1,
			"send_welcome_email": 0,
			"roles": [{"role": ATLAS_ADMIN_ROLE}],
		}
	).insert(ignore_permissions=True)
