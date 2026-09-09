from __future__ import annotations

import frappe

from atlas.auth.roles import has_role
from atlas.auth.token import CentralTokenValidator
from atlas.auth.user import CENTRAL_ADMIN_USER

ATLAS_API_PREFIX = "/api/atlas"
GUEST_PATHS = frozenset({"/login", "/api/method/login", "/api/method/logout"})
GUEST_PATH_PREFIXES = ("/assets/",)


def validate_auth() -> None:
	"""Accept one central token, then restrict a guest and an Atlas Admin to their routes."""
	authenticate_central_token()
	path = frappe.request.path.rstrip("/") or "/"

	if frappe.session.user in ("", "Guest"):
		validate_guest_path(path)
		return

	if has_role("System Manager"):
		return

	if path == ATLAS_API_PREFIX or path.startswith(f"{ATLAS_API_PREFIX}/"):
		# Atlas routes perform their own resource and tenant permission checks.
		if not has_role("Atlas Admin"):
			raise frappe.PermissionError
		return

	if has_role("Atlas Admin"):
		raise frappe.PermissionError


def validate_guest_path(path: str) -> None:
	"""Allow a guest to sign in and to read the API reference, and nothing else."""
	if path in GUEST_PATHS or path.startswith(GUEST_PATH_PREFIXES):
		return

	if path == "/api/atlas/docs" or path.startswith("/api/atlas/docs/"):
		return

	raise frappe.PermissionError


def authenticate_central_token() -> None:
	"""Sign in as the central admin user when the request carries a valid central token."""
	if frappe.session.user not in ("", "Guest"):
		return

	token = frappe.get_request_header("X-Atlas-Central-Token")
	if not token:
		return

	if CentralTokenValidator().get_claims(token) is None:
		return

	frappe.set_user(CENTRAL_ADMIN_USER)
