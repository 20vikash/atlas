from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core.placement.transaction import (
	LOCK_WAIT_SECONDS,
	is_read_committed,
	start_read_committed_transaction,
	use_read_committed,
)


class TestPlacementTransaction(UnitTestCase):
	def setUp(self) -> None:
		frappe.local.flags.pop("is_read_committed", None)
		self.addCleanup(frappe.local.flags.pop, "is_read_committed", None)

	def test_isolation_switch_ends_the_transaction_that_setup_opened(self) -> None:
		with (
			patch("atlas.vm.core.placement.transaction.frappe.db.transaction_writes", 0),
			patch("atlas.vm.core.placement.transaction.frappe.db.sql") as sql,
			patch("atlas.vm.core.placement.transaction.frappe.db.rollback") as rollback,
			patch("atlas.vm.core.placement.transaction.frappe.db.commit") as commit,
		):
			start_read_committed_transaction()

		self.assertEqual(
			[call.args for call in sql.call_args_list],
			[
				("SET SESSION innodb_lock_wait_timeout = %s", LOCK_WAIT_SECONDS),
				("SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED",),
			],
		)
		self.assertLessEqual(LOCK_WAIT_SECONDS, 1)
		rollback.assert_called_once_with()
		commit.assert_not_called()

	def test_isolation_switch_keeps_a_transaction_that_already_wrote(self) -> None:
		with (
			patch("atlas.vm.core.placement.transaction.frappe.db.transaction_writes", 1),
			patch("atlas.vm.core.placement.transaction.frappe.db.sql") as sql,
			patch("atlas.vm.core.placement.transaction.frappe.db.rollback") as rollback,
		):
			start_read_committed_transaction()

		sql.assert_not_called()
		rollback.assert_not_called()

	def test_decorator_starts_read_committed_before_the_operation(self) -> None:
		calls = []

		@use_read_committed
		def operation(value: str) -> str:
			calls.append("operation")
			return value

		with patch(
			"atlas.vm.core.placement.transaction.start_read_committed_transaction",
			side_effect=lambda: calls.append("isolation"),
		):
			self.assertEqual(operation("result"), "result")

		self.assertEqual(calls, ["isolation", "operation"])
		self.assertEqual(operation.__name__, "operation")

	def test_isolation_is_reported_from_the_session(self) -> None:
		for level, expected in (("READ-COMMITTED", True), ("REPEATABLE-READ", False)):
			frappe.local.flags.pop("is_read_committed", None)
			with patch("atlas.vm.core.placement.transaction.frappe.db.sql", return_value=[[level]]):
				self.assertIs(is_read_committed(), expected)

	def test_isolation_is_asked_once_for_each_request(self) -> None:
		frappe.local.flags.pop("is_read_committed", None)
		with patch(
			"atlas.vm.core.placement.transaction.frappe.db.sql", return_value=[["READ-COMMITTED"]]
		) as sql:
			self.assertTrue(is_read_committed())
			self.assertTrue(is_read_committed())

		sql.assert_called_once()
