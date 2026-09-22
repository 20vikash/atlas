"""Transaction settings for VM placement."""

from __future__ import annotations

from collections.abc import Callable
from functools import wraps

import frappe

READ_COMMITTED = "READ-COMMITTED"
LOCK_WAIT_SECONDS = 1


def use_read_committed[**Parameters, Result](
	function: Callable[Parameters, Result],
) -> Callable[Parameters, Result]:
	"""Run a placement entry point in a READ COMMITTED transaction."""

	@wraps(function)
	def wrapper(*args: Parameters.args, **kwargs: Parameters.kwargs) -> Result:
		start_read_committed_transaction()
		return function(*args, **kwargs)

	return wrapper


def start_read_committed_transaction() -> None:
	"""Start READ COMMITTED before placement reads capacity."""
	if frappe.db.transaction_writes:
		return

	frappe.db.sql("SET SESSION innodb_lock_wait_timeout = %s", LOCK_WAIT_SECONDS)
	frappe.db.sql("SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED")
	# Frappe may open a transaction before placement.
	frappe.db.rollback()
	frappe.local.flags.is_read_committed = True


def is_read_committed() -> bool:
	"""Return whether the current session uses READ COMMITTED."""
	if frappe.local.flags.is_read_committed is None:
		frappe.local.flags.is_read_committed = (
			frappe.db.sql("SELECT @@transaction_isolation")[0][0] == READ_COMMITTED
		)

	return frappe.local.flags.is_read_committed
