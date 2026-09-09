from __future__ import annotations

import frappe


class AtlasUserError(frappe.ValidationError):
	"""Report an Atlas error that the caller can read."""
