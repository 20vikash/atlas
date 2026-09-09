from __future__ import annotations

import functools
from collections.abc import Callable
from typing import Any

import frappe


def run_as_admin[Result](function: Callable[..., Result]) -> Callable[..., Result]:
	"""Run one background job as Administrator."""

	@functools.wraps(function)
	def wrapped(*args: Any, **kwargs: Any) -> Result:
		if getattr(frappe.local, "request", None) is not None:
			raise RuntimeError("Background jobs cannot run during an HTTP request.")
		previous_user = frappe.session.user
		frappe.set_user("Administrator")  # nosemgrep
		try:
			return function(*args, **kwargs)
		finally:
			frappe.set_user(previous_user)

	return wrapped
