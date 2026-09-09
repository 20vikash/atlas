from __future__ import annotations

from datetime import datetime
from unittest.mock import MagicMock, patch

from frappe.tests import UnitTestCase

from atlas.vm.core import vm_state


class TestVirtualMachineState(UnitTestCase):
	def store(self, names: list[str], stored: list[str], reported: dict) -> dict:
		"""Store one reported state set against a stubbed database."""
		results = [names, stored]

		with (
			patch.object(vm_state, "now_datetime", return_value=datetime(2026, 9, 9, 10, 0)),
			patch.object(vm_state.frappe, "get_all", side_effect=results),
			patch.object(vm_state, "insert_state") as insert_state,
			patch.object(vm_state, "update_states") as update_states,
			patch("atlas.vm.core.vm_state.frappe.db.delete") as delete,
		):
			vm_state.store_reported_states("metal-1", reported)

		return {"insert": insert_state, "update": update_states, "delete": delete}

	def test_a_first_report_inserts_and_a_later_report_updates(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002"],
			stored=["vm-00002"],
			reported={"vm-00001": {"status": "running"}, "vm-00002": {"status": "running"}},
		)

		self.assertEqual(calls["insert"].call_args.args[:2], ("vm-00001", "running"))
		self.assertEqual(calls["update"].call_args.args[:2], (["vm-00002"], "running"))

	def test_one_statement_updates_every_virtual_machine_of_one_status(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002", "vm-00003"],
			stored=["vm-00001", "vm-00002", "vm-00003"],
			reported={
				"vm-00001": {"status": "running"},
				"vm-00002": {"status": "running"},
				"vm-00003": {"status": "stopped"},
			},
		)

		self.assertEqual(calls["update"].call_count, 2)
		statuses = {call.args[1]: call.args[0] for call in calls["update"].call_args_list}
		self.assertEqual(statuses, {"running": ["vm-00001", "vm-00002"], "stopped": ["vm-00003"]})

	def test_a_virtual_machine_the_host_stops_reporting_loses_its_state(self) -> None:
		calls = self.store(
			names=["vm-00001", "vm-00002"],
			stored=["vm-00001", "vm-00002"],
			reported={"vm-00001": {"status": "running"}},
		)

		calls["delete"].assert_called_once_with("Virtual Machine State", {"name": ["in", ["vm-00002"]]})

	def test_a_host_without_virtual_machines_reads_nothing_more(self) -> None:
		get_all = MagicMock(side_effect=[[]])

		with patch.object(vm_state.frappe, "get_all", get_all):
			vm_state.store_reported_states("metal-1", {})

		get_all.assert_called_once()

	def test_an_invalid_response_is_rejected(self) -> None:
		for reported in ([], {"vm-00001": "running"}, {"vm-00001": {"status": ""}}, {"vm-00001": {}}):
			with self.assertRaises(ValueError):
				vm_state.get_reported_statuses(reported)
