from __future__ import annotations

import frappe

from atlas.api.core.errors import InvalidRequest

TENANT_HEADER = "X-Tenant-ID"
MAXIMUM_TENANT_ID = 0xFFFFFFFF


def get_tenant_id() -> int:
	"""Return the tenant of the current request."""
	request = getattr(frappe.local, "request", None)
	value = request.headers.get(TENANT_HEADER, "") if request else ""
	return parse_tenant_id(value)


def parse_tenant_id(value: str) -> int:
	"""Return one valid tenant ID."""
	if not value.strip():
		raise invalid_tenant("The request needs a tenant ID.")
	try:
		tenant_id = int(value.strip())
	except ValueError as error:
		raise invalid_tenant("The tenant ID must be a whole number.") from error

	if tenant_id == 0:
		raise invalid_tenant("Tenant 0 is reserved.")

	if not 1 <= tenant_id <= MAXIMUM_TENANT_ID:
		raise invalid_tenant("The tenant ID must be an unsigned 32-bit integer.")
	return tenant_id


def invalid_tenant(message: str) -> InvalidRequest:
	"""Return the failure for one unusable tenant header."""
	return InvalidRequest(message, fields=[{"name": TENANT_HEADER, "message": message}])
