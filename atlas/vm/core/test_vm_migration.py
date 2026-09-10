from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.vm_migration import MigrationService
from atlas.vm.doctype.virtual_machine import virtual_machine as virtual_machine_module


def source_vm(**overrides: object) -> SimpleNamespace:
	values: dict[str, object] = {
		"name": "vm-00001",
		"active_migration": None,
		"is_draft": 0,
		"is_terminating": 0,
		"current_state": "running",
	}
	values.update(overrides)
	return SimpleNamespace(**values)


class TestMigrationValidation(UnitTestCase):
	def test_rejects_a_vm_that_is_already_migrating(self) -> None:
		with self.assertRaisesRegex(AtlasUserError, "already migrating"):
			MigrationService.validate_source(source_vm(active_migration="mig-00001"))

	def test_rejects_a_draft_or_terminating_vm(self) -> None:
		with self.assertRaises(AtlasUserError):
			MigrationService.validate_source(source_vm(is_draft=1))
		with self.assertRaises(AtlasUserError):
			MigrationService.validate_source(source_vm(is_terminating=1))

	def test_rejects_a_failed_or_unknown_vm(self) -> None:
		for state in ("failed", "unknown"):
			with self.assertRaises(AtlasUserError):
				MigrationService.validate_source(source_vm(current_state=state))

	def test_accepts_a_running_stopped_or_paused_vm(self) -> None:
		for state in ("running", "stopped", "paused"):
			self.assertIsNone(MigrationService.validate_source(source_vm(current_state=state)))


class TestMigrationCreation(UnitTestCase):
	def test_create_reserves_a_target_and_locks_the_vm(self) -> None:
		locked = source_vm(
			server="metal-1",
			vcpus=2,
			memory_mib=2048,
			disk_mib=10240,
			tenant_id=7,
			virtual_machine_image="Ubuntu",
			db_set=Mock(),
		)
		inserted = Mock()
		inserted.insert.return_value = SimpleNamespace(name="mig-00001")

		def fake_get_doc(*args: object, **kwargs: object) -> object:
			return locked if args and args[0] == "Virtual Machine" else inserted

		with (
			patch("atlas.vm.core.vm_migration.frappe.get_doc", side_effect=fake_get_doc) as get_doc,
			patch(
				"atlas.vm.core.vm_migration.PlacementService.select_server",
				return_value=SimpleNamespace(name="metal-2"),
			) as select_server,
			patch("atlas.vm.core.vm_migration.frappe.db.get_value", return_value="amd64"),
			patch("atlas.vm.core.vm_migration.frappe.db.commit"),
			patch("atlas.vm.core.vm_migration.now_datetime", return_value="2026-09-10 00:00:00"),
		):
			migration_id = MigrationService.create(SimpleNamespace(name="vm-00001"))

		self.assertEqual(migration_id, "mig-00001")
		self.assertEqual(select_server.call_args.kwargs["exclude_servers"], {"metal-1"})
		inserted_fields = next(
			call.args[0] for call in get_doc.call_args_list if call.args and isinstance(call.args[0], dict)
		)
		self.assertEqual(inserted_fields["source_server"], "metal-1")
		self.assertEqual(inserted_fields["target_server"], "metal-2")
		self.assertEqual(inserted_fields["status"], "running")
		locked.db_set.assert_called_once_with("active_migration", "mig-00001")

	def test_migrate_delegates_to_the_service(self) -> None:
		virtual_machine = frappe.new_doc("Virtual Machine")
		virtual_machine.check_permission = Mock()

		with patch(
			"atlas.vm.core.vm_migration.MigrationService.create", return_value="mig-00001"
		) as create:
			result = virtual_machine.migrate()

		self.assertEqual(result, "mig-00001")
		create.assert_called_once_with(virtual_machine)


class TestMigrationActionLock(UnitTestCase):
	def test_lock_blocks_a_mutable_action(self) -> None:
		virtual_machine = frappe.new_doc("Virtual Machine")
		virtual_machine.active_migration = "mig-00001"
		with self.assertRaises(AtlasUserError):
			virtual_machine.ensure_not_migrating()

	def test_unlocked_vm_passes(self) -> None:
		virtual_machine = frappe.new_doc("Virtual Machine")
		self.assertIsNone(virtual_machine.ensure_not_migrating())

	def test_power_action_stops_before_metal_during_a_migration(self) -> None:
		virtual_machine = frappe.new_doc("Virtual Machine")
		virtual_machine.active_migration = "mig-00001"
		virtual_machine.check_permission = Mock()

		with patch.object(virtual_machine_module, "VirtualMachineService") as service:
			with self.assertRaises(AtlasUserError):
				virtual_machine.set_power_state("running")

		service.assert_not_called()

	def test_reads_stay_open_during_a_migration(self) -> None:
		virtual_machine = frappe.new_doc("Virtual Machine")
		virtual_machine.active_migration = "mig-00001"
		self.assertEqual(virtual_machine.current_state, "unknown")
