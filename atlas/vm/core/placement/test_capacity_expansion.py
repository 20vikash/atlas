from contextlib import contextmanager
from types import SimpleNamespace
from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core.placement.capacity_expansion import ensure_capacity


class TestCapacityExpansion(UnitTestCase):
	def test_pending_server_prevents_another_provision(self) -> None:
		settings = SimpleNamespace(
			auto_spawn_metal_server=True,
			default_metal_machine_size="large",
			default_metal_machine_image="ubuntu",
			server_provider="AWS",
		)
		size = SimpleNamespace(
			name="large",
			enabled=True,
			architecture="amd64",
			memory_mib=8192,
			disk_gib=100,
		)
		image = SimpleNamespace(name="ubuntu", enabled=True)

		with (
			patch("atlas.vm.core.placement.capacity_expansion.frappe.get_single", return_value=settings),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.db.exists",
				side_effect=[True, True, "metal-1"],
			),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.get_doc",
				side_effect=[size, image],
			),
			patch("atlas.vm.core.placement.capacity_expansion.frappe.db.advisory_lock") as lock,
			patch("atlas.vm.core.placement.capacity_expansion.frappe.db.rollback") as rollback,
			patch("atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision") as provision,
			patch("atlas.vm.core.placement.capacity_expansion.frappe.db.commit") as commit,
		):
			ensure_capacity("amd64", 4096, 51200, is_sleepy=False)

		lock.assert_called_once_with(f"{frappe.db.cur_db_name}:ensure-capacity:amd64:0")
		rollback.assert_called_once_with()
		provision.assert_not_called()
		commit.assert_not_called()

	def test_new_server_is_committed_before_the_lock_is_released(self) -> None:
		events = []
		settings = SimpleNamespace(
			auto_spawn_metal_server=True,
			default_metal_machine_size="large",
			default_metal_machine_image="ubuntu",
			server_provider="AWS",
		)
		size = SimpleNamespace(
			name="large",
			enabled=True,
			architecture="amd64",
			memory_mib=8192,
			disk_gib=100,
		)
		image = SimpleNamespace(name="ubuntu", enabled=True)

		@contextmanager
		def lock(key: str):
			events.append(("lock", key))
			try:
				yield
			finally:
				events.append(("unlock", key))

		with (
			patch("atlas.vm.core.placement.capacity_expansion.frappe.get_single", return_value=settings),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.db.exists",
				side_effect=[True, True, None],
			),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.get_doc",
				side_effect=[size, image],
			),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.db.advisory_lock",
				side_effect=lock,
			),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.db.rollback",
				side_effect=lambda: events.append(("rollback", None)),
			),
			patch(
				"atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision",
				side_effect=lambda **kwargs: events.append(("provision", kwargs)),
			),
			patch(
				"atlas.vm.core.placement.capacity_expansion.frappe.db.commit",
				side_effect=lambda: events.append(("commit", None)),
			),
		):
			ensure_capacity("amd64", 4096, 51200, is_sleepy=False)

		self.assertEqual(
			[event[0] for event in events],
			["lock", "rollback", "provision", "commit", "unlock"],
		)
