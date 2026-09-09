from __future__ import annotations

from datetime import datetime
from typing import Any

import frappe
from frappe.utils import now_datetime


def store_reported_states(server_name: str, reported: object) -> None:
	"""Replace the stored virtual machine states of one host."""
	statuses = get_reported_statuses(reported)
	names = frappe.get_all("Virtual Machine", filters={"server": server_name}, pluck="name")
	if not names:
		return

	stored = set(frappe.get_all("Virtual Machine State", filters={"name": ["in", names]}, pluck="name"))
	synced_at = now_datetime()

	updated_names: dict[str, list[str]] = {}
	for name in names:
		status = statuses.get(name)
		if status is None:
			continue
		if name in stored:
			updated_names.setdefault(status, []).append(name)
		else:
			insert_state(name, status, synced_at)

	for status, group in updated_names.items():
		update_states(group, status, synced_at)

	absent = [name for name in names if name in stored and name not in statuses]
	if absent:
		frappe.db.delete("Virtual Machine State", {"name": ["in", absent]})


def get_reported_statuses(reported: object) -> dict[str, str]:
	"""Return the status of each virtual machine in a Metal sync response."""
	if not isinstance(reported, dict):
		raise ValueError("Metal virtual machine response must be an object")

	statuses = {}
	for name, state in reported.items():
		status = state.get("status") if isinstance(state, dict) else None
		if not isinstance(status, str) or not status:
			raise ValueError("Metal virtual machine response has invalid values")
		statuses[name] = status
	return statuses


def insert_state(name: str, status: str, synced_at: datetime) -> None:
	"""Store the first reported state of one virtual machine."""
	frappe.get_doc(
		{
			"doctype": "Virtual Machine State",
			"virtual_machine": name,
			"status": status,
			"synced_at": synced_at,
		}
	).insert(ignore_permissions=True)


def update_states(names: list[str], status: str, synced_at: datetime) -> None:
	"""Store one status for every named virtual machine in one statement."""
	table = frappe.qb.DocType("Virtual Machine State")
	(
		frappe.qb.update(table)
		.set(table.status, status)
		.set(table.synced_at, synced_at)
		.where(table.name.isin(names))
	).run()


def get_reported_state_rows(names: list[str]) -> dict[str, Any]:
	"""Return the stored state row of each named virtual machine."""
	if not names:
		return {}

	rows = frappe.get_all(
		"Virtual Machine State",
		filters={"name": ["in", names]},
		fields=["name", "status", "synced_at"],
	)
	return {row.name: row for row in rows}
