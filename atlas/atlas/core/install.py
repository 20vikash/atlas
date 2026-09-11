from __future__ import annotations

import frappe


def complete_setup_wizard() -> None:
	from frappe.desk.page.setup_wizard.setup_wizard import setup_complete

	setup_complete({})

	if not frappe.get_system_settings("time_zone"):
		frappe.db.set_single_value("System Settings", "time_zone", "UTC")
