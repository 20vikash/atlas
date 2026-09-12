from __future__ import annotations

import click
import frappe
from frappe.query_builder.functions import Coalesce
from frappe.utils import now_datetime


def complete_setup_wizard() -> None:
	from frappe.desk.page.setup_wizard.setup_wizard import setup_complete

	setup_complete({})

	if not frappe.get_system_settings("time_zone"):
		frappe.db.set_single_value("System Settings", "time_zone", "UTC")

	realign_scheduled_job_baselines()


def realign_scheduled_job_baselines() -> int:
	"""Bring back jobs whose baseline sits in the future, and return how many were moved.

	A site starts with no System Settings time zone, so Frappe stamps the job types that
	`bench new-site` creates in its hardcoded Asia/Kolkata fallback. Atlas then moves the
	site to UTC, which leaves those stamps ahead of the clock. A cold job has no
	`last_execution` and falls back to `creation`, so the scheduler finds nothing due until
	real time catches up with the stamp.
	"""
	now = now_datetime()
	table = frappe.qb.DocType("Scheduled Job Type")
	stale = (
		frappe.qb.from_(table).select(table.name).where(Coalesce(table.last_execution, table.creation) > now)
	).run(pluck=True)

	if not stale:
		return 0

	(frappe.qb.update(table).set(table.last_execution, now).where(table.name.isin(stale))).run()
	click.echo(f"Realigned {len(stale)} scheduled job baselines left ahead of the clock.")

	return len(stale)
