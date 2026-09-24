from __future__ import annotations

import frappe


class AtlasUserError(frappe.ValidationError):
	"""Report an Atlas error that the caller can read."""


class AtlasConflictError(AtlasUserError):
	"""Report that the resource state does not allow the request."""

	http_status_code = 409
