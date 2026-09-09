from __future__ import annotations

import frappe


def has_role(role: str, user: str | None = None) -> bool:
	"""Return whether a user has one role."""
	user = user or frappe.session.user
	if not user or user == "Guest":
		return False

	return role in frappe.get_roles(user)
