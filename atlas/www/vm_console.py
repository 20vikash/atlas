from __future__ import annotations

import frappe

no_cache = 1


def get_context(context):
	"""Build the console page context."""
	context.no_cache = 1
	context.show_sidebar = False
	context.sitename = frappe.local.site
	context.socketio_port = frappe.get_common_site_config().get("socketio_port") or 9000
	return context
