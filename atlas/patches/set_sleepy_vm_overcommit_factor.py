import frappe


def execute() -> None:
	if frappe.db.get_singles_dict("Atlas Settings").get("sleepy_vm_overcommit_factor") is None:
		frappe.db.set_single_value("Atlas Settings", "sleepy_vm_overcommit_factor", 1.0)
