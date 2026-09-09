from __future__ import annotations

import frappe

from atlas.auth.roles import has_role


def validate_auth() -> None:
	"""Restrict an Atlas Admin to Atlas API routes."""
	if not has_role("Atlas Admin") or has_role("System Manager"):
		return

	path = frappe.request.path.rstrip("/") or "/"
	if path == "/api/atlas" or path.startswith("/api/atlas/"):
		return

	raise frappe.PermissionError
