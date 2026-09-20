from __future__ import annotations

from datetime import datetime
from types import SimpleNamespace
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.vm_migration import MigrationService


def migration_doc(**overrides: object) -> SimpleNamespace:
	"""Build the migration fields used by unit tests."""
	values: dict[str, object] = {
		"name": "mig-00001",
		"virtual_machine": "vm-00001",
		"source_metal_server": "metal-1",
		"destination_metal_server": "metal-2",
		"status": "preparing",
		"started_at": None,
		"progress_percent": 0,
		"destination_metal_server_selection_attempts": 0,
		"transfers": [],
	}
	values.update(overrides)
	return SimpleNamespace(**values)


def source_vm(**overrides: object) -> SimpleNamespace:
	"""Build a VM source shape used by validation tests."""
	values: dict[str, object] = {
		"name": "vm-00001",
		"active_migration": None,
		"is_draft": 0,
		"is_terminating": 0,
		"current_state": "running",
		"architecture": "amd64",
	}
	values.update(overrides)
	return SimpleNamespace(**values)


class TestMigrationValidation(UnitTestCase):
	def test_rejects_an_already_migrating_vm(self) -> None:
		with self.assertRaisesRegex(AtlasUserError, "already migrating"):
			MigrationService.validate_source(source_vm(active_migration="mig-00001"))

	def test_rejects_a_vm_without_a_live_state(self) -> None:
		for state in ("failed", "unknown"):
			with self.assertRaises(AtlasUserError):
				MigrationService.validate_source(source_vm(current_state=state))


class TestMigrationStatus(UnitTestCase):
	def test_maps_metal_phases_to_lifecycle_statuses(self) -> None:
		service = MigrationService(migration_doc())
		self.assertEqual(service.status_from_response({"status": "running", "phase": "copying"}), "copying")
		self.assertEqual(
			service.status_from_response({"status": "running", "phase": "stopping"}), "cutting_over"
		)
		self.assertEqual(service.status_from_response({"status": "ready"}), "finalizing")

	def test_calculates_copying_progress_from_rounds(self) -> None:
		service = MigrationService(migration_doc())
		self.assertEqual(service.progress_percent("preparing", []), 5)
		self.assertEqual(
			service.progress_percent(
				"copying",
				[
					{"completed": True},
					{"completed": False, "transferred_mib": 50, "total_mib": 100},
				],
			),
			15,
		)
		self.assertEqual(service.progress_percent("starting", []), 90)

	def test_aborted_destination_becomes_failed_after_a_metal_error(self) -> None:
		service = MigrationService(migration_doc(error_code="migration_error"))
		service.settle = Mock()
		self.assertTrue(service.advance({"status": "aborted"}))
		service.settle.assert_called_once_with("failed")


class TestDestinationMetalServerSelection(UnitTestCase):
	def test_automatic_selection_retries_after_a_capacity_error(self) -> None:
		service = MigrationService(migration_doc(destination_metal_server=None))
		service.record_destination_metal_server_selection_failure = Mock()
		with (
			patch("atlas.vm.core.vm_migration.frappe.get_doc", return_value=source_vm()),
			patch("atlas.vm.core.vm_migration.MigrationService.get_shape") as get_shape,
			patch(
				"atlas.vm.core.vm_migration.PlacementStrategy.find_server",
				side_effect=AtlasUserError("out of capacity"),
			),
		):
			get_shape.return_value = SimpleNamespace(
				cpu_millicores=1000,
				memory_mib=512,
				disk_mib=1024,
				tenant_id=7,
				sleep_after_idle_seconds=0,
			)
			self.assertFalse(service.select_destination_metal_server())
		service.record_destination_metal_server_selection_failure.assert_called_once()

	def test_reserves_a_destination_stored_on_the_scheduled_migration(self) -> None:
		service = MigrationService(migration_doc(status="scheduled", destination_metal_server="metal-3"))
		service.update = Mock()

		with (
			patch("atlas.vm.core.vm_migration.frappe.get_doc", return_value=source_vm()),
			patch("atlas.vm.core.vm_migration.now_datetime", return_value="2026-09-21 12:00:00"),
			patch("atlas.vm.core.vm_migration.MigrationService.get_shape") as get_shape,
			patch(
				"atlas.vm.core.vm_migration.PlacementStrategy.reserve_server",
				return_value="metal-3",
			) as reserve_server,
		):
			get_shape.return_value = SimpleNamespace(
				cpu_millicores=1000,
				memory_mib=512,
				disk_mib=1024,
				tenant_id=7,
				sleep_after_idle_seconds=0,
			)
			self.assertTrue(service.select_destination_metal_server())

		reserve_server.assert_called_once()
		self.assertEqual(service.update.call_args.args[0]["destination_metal_server"], "metal-3")
		self.assertEqual(service.update.call_args.args[0]["status"], "preparing")

	def test_scheduled_abort_does_not_contact_an_unreserved_destination(self) -> None:
		service = MigrationService(migration_doc(status="scheduled", destination_metal_server="metal-2"))
		service.settle = Mock()
		service.request_destination_abort = Mock()

		service.request_abort()

		service.settle.assert_called_once_with("aborted")
		service.request_destination_abort.assert_not_called()


class TestMigrationTransfers(UnitTestCase):
	def test_upserts_each_transfer_into_its_row_index(self) -> None:
		class Document(SimpleNamespace):
			def append(self, _: str, values: dict[str, object]) -> SimpleNamespace:
				row = SimpleNamespace(**values)
				self.transfers.append(row)
				return row

		migration = Document(transfers=[])
		with patch("frappe.utils.data.get_system_timezone", return_value="UTC"):
			MigrationService.upsert_transfers(
				migration,
				[
					{
						"sequence": 1,
						"finished_at": "2026-09-20T12:00:42.02675576Z",
						"transferred_mib": 100,
						"total_mib": 100,
						"completed": True,
					},
					{
						"sequence": 2,
						"finished_at": "0001-01-01T00:00:00Z",
						"transferred_mib": 40,
						"total_mib": 100,
						"completed": False,
					},
				],
			)
			self.assertIsNone(migration.transfers[1].finished_at)

			MigrationService.upsert_transfers(
				migration,
				[
					{
						"sequence": 2,
						"finished_at": "2026-09-20T12:01:10Z",
						"transferred_mib": 100,
						"total_mib": 100,
						"completed": True,
					}
				],
			)

		self.assertEqual(len(migration.transfers), 2)
		self.assertEqual([row.idx for row in migration.transfers], [1, 2])
		self.assertEqual(migration.transfers[0].finished_at, datetime(2026, 9, 20, 12, 0, 42, 26755))
		self.assertEqual(migration.transfers[1].finished_at, datetime(2026, 9, 20, 12, 1, 10))
		self.assertEqual(migration.transfers[1].transferred_mib, 100)
		self.assertTrue(migration.transfers[1].completed)
