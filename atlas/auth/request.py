from __future__ import annotations

import frappe

from atlas.auth.roles import has_role
from atlas.auth.token import CentralTokenValidator
from atlas.auth.user import CENTRAL_ADMIN_USER

BEARER_PREFIX = "bearer"


def validate_auth() -> None:
	"""Accept one central token, then restrict an Atlas Admin to Atlas API routes."""
	authenticate_central_token()
	if not has_role("Atlas Admin") or has_role("System Manager"):
		return

	path = frappe.request.path.rstrip("/") or "/"
	if path == "/api/atlas" or path.startswith("/api/atlas/"):
		return

	raise frappe.PermissionError


def authenticate_central_token() -> None:
	"""Sign in as the Atlas Admin user when the request carries a valid central token."""
	if frappe.session.user not in ("", "Guest"):
		return

	header = frappe.get_request_header("Authorization", "").split(" ")
	if len(header) != 2 or header[0].lower() != BEARER_PREFIX:
		return

	if CentralTokenValidator().get_claims(header[1]) is None:
		return

	frappe.set_user(CENTRAL_ADMIN_USER)
