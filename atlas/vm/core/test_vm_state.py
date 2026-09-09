from __future__ import annotations

from datetime import datetime
from unittest.mock import MagicMock, patch

from frappe.tests import UnitTestCase

from atlas.vm.core import vm_state


class TestVirtualMachineState(UnitTestCase):
	def store(
		self, names: list[str], stored: list[str], reported: dict, save_error: Exception | None = None
	) -> dict:
		"""Store one reported state set against a stubbed database."""
		saved = []

		def make_document(*args) -> MagicMock:
			document = MagicMock()
			document.save.side_effect = save_error
			saved.append(document)
			return document

		with (
			patch.object(vm_state, "now_datetime", return_value=datetime(2026, 9, 9, 10, 0)),
			patch.object(vm_state.frappe, "get_all", side_effect=[names, stored]),
			patch.object(vm_state.frappe, "get_doc", side_effect=make_document),
			patch.object(vm_state.frappe, "new_doc", side_effect=make_document),
			patch.object(vm_state.frappe, "log_error"),
			patch.object(vm_state.frappe.db, "commit") as commit,
		):
			vm_state.store_reported_states("metal-1", reported)

		return {"saved": saved, "commit": commit}

	def test_a_first_report_inserts_and_a_later_report_updates(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002"],
			stored=["vm-00002"],
			reported={"vm-00001": {"status": "running"}, "vm-00002": {"status": "running"}},
		)

		inserted, updated = calls["saved"]
		self.assertEqual(inserted.virtual_machine, "vm-00001")
		self.assertEqual(updated.status, "running")
		self.assertEqual(updated.synced_at, datetime(2026, 9, 9, 10, 0))

	def test_every_reported_virtual_machine_is_saved_as_a_document(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002", "vm-00003"],
			stored=["vm-00001", "vm-00002", "vm-00003"],
			reported={
				"vm-00001": {"status": "running"},
				"vm-00002": {"status": "running"},
				"vm-00003": {"status": "stopped"},
			},
		)

		self.assertEqual([document.status for document in calls["saved"]], ["running", "running", "stopped"])
		self.assertEqual(calls["commit"].call_count, 3)

	def test_an_unreported_virtual_machine_keeps_its_stored_state(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002"],
			stored=["vm-00001", "vm-00002"],
			reported={"vm-00001": {"status": "running"}},
		)

		self.assertEqual(len(calls["saved"]), 1)

	def test_a_host_without_virtual_machines_reads_nothing_more(self) -> None:
		get_all = MagicMock(side_effect=[[]])

		with patch.object(vm_state.frappe, "get_all", get_all):
			vm_state.store_reported_states("metal-1", {})

		get_all.assert_called_once()

	def test_an_invalid_response_is_rejected(self) -> None:
		for reported in ([], {"vm-00001": "running"}, {"vm-00001": {"status": ""}}, {"vm-00001": {}}):
			with self.assertRaises(ValueError):
				vm_state.get_reported_statuses(reported)

	def test_one_failed_write_does_not_stop_the_other_writes(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002"],
			stored=["vm-00001", "vm-00002"],
			reported={"vm-00001": {"status": "running"}, "vm-00002": {"status": "running"}},
			save_error=ValueError("boom"),
		)

		self.assertEqual(len(calls["saved"]), 2)
		self.assertEqual(calls["commit"].call_count, 0)
