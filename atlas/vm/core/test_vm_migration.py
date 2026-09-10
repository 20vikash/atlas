from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import Mock, PropertyMock, call, patch

import frappe
from frappe.tests import UnitTestCase
from frappe.utils import add_to_date, now_datetime

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.metal_client import MetalClientError
from atlas.vm.core.vm_migration import MigrationService, reconcile_migrations
from atlas.vm.doctype.virtual_machine import virtual_machine as virtual_machine_module


def migration_doc(**overrides: object) -> SimpleNamespace:
	values: dict[str, object] = {
		"name": "mig-00001",
		"virtual_machine": "vm-00001",
		"source_server": "metal-1",
		"target_server": "metal-2",
		"status": "running",
		"abort_requested": 0,
		"progress": "{}",
		"db_set": Mock(),
	}
	values.update(overrides)
	return SimpleNamespace(**values)


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


class TestMigrationWorker(UnitTestCase):
	def test_poll_interval_speeds_up_for_transitions(self) -> None:
		self.assertEqual(MigrationService.poll_interval({"phase": "copying"}), 5)
		self.assertEqual(MigrationService.poll_interval({}), 5)
		self.assertEqual(MigrationService.poll_interval({"phase": "stopping"}), 2)
		self.assertEqual(MigrationService.poll_interval({"phase": "starting"}), 2)

	def test_run_commits_the_server_before_it_finishes(self) -> None:
		service = MigrationService(migration_doc())
		manager = Mock()
		service.send_request = manager.send_request
		service.commit_target = manager.commit_target
		service.finish = manager.finish
		service.settle = manager.settle
		service.poll = Mock(
			side_effect=[{"status": "running"}, {"status": "ready"}, {"status": "completed"}]
		)

		with (
			patch.object(
				MigrationService, "is_target_committed", new_callable=PropertyMock, return_value=False
			),
			patch("atlas.vm.core.vm_migration.time.sleep"),
		):
			service.run()

		self.assertEqual(
			manager.mock_calls,
			[call.send_request(), call.commit_target(), call.finish(), call.settle("completed")],
		)

	def test_run_skips_the_request_when_an_abort_is_pending(self) -> None:
		service = MigrationService(migration_doc(abort_requested=1))
		service.send_request = Mock()
		service.request_abort = Mock()
		service.settle = Mock()
		service.poll = Mock(side_effect=[{"status": "aborted"}])

		with (
			patch.object(
				MigrationService, "is_target_committed", new_callable=PropertyMock, return_value=False
			),
			patch("atlas.vm.core.vm_migration.time.sleep"),
		):
			service.run()

		service.send_request.assert_not_called()
		service.settle.assert_called_once_with("aborted")

	def test_failure_marks_failed_and_requests_abort(self) -> None:
		service = MigrationService(migration_doc())
		service.mark_failed = Mock()
		service.request_abort = Mock()

		self.assertFalse(service.advance({"status": "failed"}))
		service.mark_failed.assert_called_once()
		service.request_abort.assert_called_once()

	def test_commit_target_changes_the_server_once(self) -> None:
		service = MigrationService(migration_doc())
		locked_vm = SimpleNamespace(server="metal-1", db_set=Mock())
		locked_migration = migration_doc(status="running")

		def fake_get_doc(doctype: str, *args: object, **kwargs: object) -> object:
			return locked_vm if doctype == "Virtual Machine" else locked_migration

		with (
			patch("atlas.vm.core.vm_migration.frappe.get_doc", side_effect=fake_get_doc),
			patch("atlas.vm.core.vm_migration.frappe.db.commit"),
		):
			service.commit_target()

		locked_vm.db_set.assert_called_once_with("server", "metal-2")
		locked_migration.db_set.assert_called_once_with("status", "ready")

	def test_commit_target_is_idempotent(self) -> None:
		service = MigrationService(migration_doc())
		locked_vm = SimpleNamespace(server="metal-2", db_set=Mock())
		locked_migration = migration_doc(status="ready")

		def fake_get_doc(doctype: str, *args: object, **kwargs: object) -> object:
			return locked_vm if doctype == "Virtual Machine" else locked_migration

		with (
			patch("atlas.vm.core.vm_migration.frappe.get_doc", side_effect=fake_get_doc),
			patch("atlas.vm.core.vm_migration.frappe.db.commit"),
		):
			service.commit_target()

		locked_vm.db_set.assert_not_called()
		locked_migration.db_set.assert_not_called()

	def test_request_abort_records_a_failed_abort(self) -> None:
		service = MigrationService(migration_doc())
		client = Mock()
		client.abort_migration.side_effect = MetalClientError("host busy", status=503)
		service.record_error = Mock()

		with (
			patch.object(
				MigrationService, "target_client", new_callable=PropertyMock, return_value=client
			),
			patch("atlas.vm.core.vm_migration.frappe.db.commit"),
		):
			service.request_abort()

		service.migration.db_set.assert_called_once_with("abort_requested", 1)
		service.record_error.assert_called_once()

	def test_abort_records_intent_and_queues_the_worker(self) -> None:
		migration = frappe.new_doc("Virtual Machine Migration")
		migration.virtual_machine = "vm-00001"
		migration.name = "mig-00001"
		migration.db_set = Mock()

		with (
			patch("atlas.vm.core.vm_migration.frappe.get_doc", return_value=Mock()),
			patch("atlas.vm.core.vm_migration.frappe.db.commit"),
			patch("atlas.vm.core.vm_migration.enqueue_migration") as enqueue,
		):
			migration.abort()

		migration.db_set.assert_called_once_with("abort_requested", 1)
		enqueue.assert_called_once_with("mig-00001")

	def test_enqueue_deduplicates_per_migration(self) -> None:
		from atlas.vm.core.vm_migration import enqueue_migration

		with patch("atlas.vm.core.vm_migration.frappe.enqueue") as enqueue:
			enqueue_migration("mig-00001")

		self.assertEqual(enqueue.call_args.kwargs["job_id"], "atlas||vm-migration||mig-00001")
		self.assertTrue(enqueue.call_args.kwargs["deduplicate"])


class TestMigrationRecovery(UnitTestCase):
	def test_reconcile_requeues_every_locked_migration(self) -> None:
		with (
			patch(
				"atlas.vm.core.vm_migration.frappe.get_all", return_value=["mig-00001", "mig-00002"]
			),
			patch("atlas.vm.core.vm_migration.enqueue_migration") as enqueue,
		):
			reconcile_migrations()

		self.assertEqual(
			enqueue.call_args_list, [call("mig-00001"), call("mig-00002")]
		)

	def test_missing_target_retries_before_the_timeout(self) -> None:
		service = MigrationService(migration_doc())
		service.send_request = Mock()

		with patch.object(
			MigrationService, "is_expired", new_callable=PropertyMock, return_value=False
		):
			self.assertFalse(service.advance({"status": "missing"}))

		service.send_request.assert_called_once()

	def test_missing_target_expires_after_the_timeout(self) -> None:
		service = MigrationService(migration_doc())
		service.expire = Mock()

		with patch.object(
			MigrationService, "is_expired", new_callable=PropertyMock, return_value=True
		):
			self.assertTrue(service.advance({"status": "missing"}))

		service.expire.assert_called_once()

	def test_expire_aborts_the_remnant_and_releases_the_lock(self) -> None:
		service = MigrationService(migration_doc())
		client = Mock()
		service.settle = Mock()

		with patch.object(
			MigrationService, "target_client", new_callable=PropertyMock, return_value=client
		):
			service.expire()

		client.abort_migration.assert_called_once_with("mig-00001")
		service.settle.assert_called_once_with("failed")

	def test_poll_reports_a_missing_target(self) -> None:
		service = MigrationService(migration_doc())
		client = Mock()
		client.get_migration.side_effect = MetalClientError("gone", status=404)

		with patch.object(
			MigrationService, "target_client", new_callable=PropertyMock, return_value=client
		):
			status = service.poll()

		self.assertEqual(status, {"status": "missing"})
		service.migration.db_set.assert_not_called()

	def test_is_expired_tracks_the_visibility_timeout(self) -> None:
		recent = MigrationService(migration_doc(started_at=now_datetime()))
		stale = MigrationService(migration_doc(started_at=add_to_date(now_datetime(), minutes=-11)))

		self.assertFalse(recent.is_expired)
		self.assertTrue(stale.is_expired)
