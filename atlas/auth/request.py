from __future__ import annotations

import frappe

from atlas.auth.roles import has_role
from atlas.auth.token import CentralTokenValidator
from atlas.auth.user import CENTRAL_ADMIN_USER

ATLAS_API_PREFIX = "/api/atlas"
ATLAS_DOCS_PREFIX = "/api/atlas/docs"
REALTIME_PREFIX = "/socket.io"
REALTIME_PATHS = frozenset(
	{
		"/api/method/frappe.realtime.get_user_info",
		"/api/method/frappe.realtime.has_permission",
	}
)
GUEST_PATHS = frozenset({"/login", "/api/method/login", "/api/method/logout"})
GUEST_PATH_PREFIXES = ("/assets/",)


def validate_auth() -> None:
	"""Accept a central token, allow realtime support paths, and enforce route access."""
	authenticate_central_token()
	path = frappe.request.path.rstrip("/") or "/"
	if is_realtime_path(path):
		return

	if frappe.session.user in ("", "Guest"):
		validate_guest_path(path)
		return

	if has_role("System Manager"):
		return

	if is_path_within(path, ATLAS_API_PREFIX):
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

	if is_path_within(path, ATLAS_DOCS_PREFIX):
		return

	raise frappe.PermissionError


def is_realtime_path(path: str) -> bool:
	"""Return whether a path serves Socket.IO or its web-process permission calls."""
	return path in REALTIME_PATHS or is_path_within(path, REALTIME_PREFIX)


def is_path_within(path: str, prefix: str) -> bool:
	"""Return whether a path is the prefix or one of its children."""
	return path == prefix or path.startswith(f"{prefix}/")


def authenticate_central_token() -> None:
	"""Sign in as the central admin user when the request carries a valid central token."""
	if frappe.session.user not in ("", "Guest"):
		return

	token = frappe.get_request_header("X-Atlas-Central-Token")
	if not token:
		return

	if CentralTokenValidator().get_claims(token) is None:
		return

	frappe.set_user(CENTRAL_ADMIN_USER)  # nosemgrep
