import frappe


def execute() -> None:
	if frappe.db.get_singles_dict("Atlas Settings").get("placement_strategy") is None:
		frappe.db.set_single_value("Atlas Settings", "placement_strategy", "Default")
