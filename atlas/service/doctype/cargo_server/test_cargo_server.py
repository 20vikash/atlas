# Copyright (c) 2026, Frappe and Contributors
# See license.txt

from contextlib import nullcontext
from types import SimpleNamespace
from unittest.mock import MagicMock, Mock, patch

import frappe
from frappe.tests import UnitTestCase

import atlas.service.doctype.cargo_server.cargo_server as cargo_server_module
from atlas.service.doctype.cargo_server.cargo_server import CargoServer


class TestCargoServerProvisioning(UnitTestCase):
	def test_provisioning_requires_an_active_proxy(self) -> None:
		server = SimpleNamespace()
		with (
			patch.object(cargo_server_module.frappe.db, "exists", return_value=None),
			self.assertRaisesRegex(frappe.ValidationError, "Active Proxy Server"),
		):
			CargoServer._validate_active_proxy(server)

	def test_provisioning_requires_a_system_image(self) -> None:
		server = SimpleNamespace()
		with (
			patch.object(cargo_server_module.frappe.db, "get_value", return_value="machine"),
			self.assertRaisesRegex(frappe.ValidationError, "System Virtual Machine Image"),
		):
			CargoServer._validate_system_image(server, "image-1")

	def test_an_attached_virtual_machine_blocks_provisioning(self) -> None:
		server = SimpleNamespace(virtual_machine="vm-00001", status="Failed")
		with self.assertRaisesRegex(frappe.ValidationError, "Archive"):
			CargoServer._validate_provisioning_state(server)

	def test_a_pending_state_blocks_provisioning(self) -> None:
		server = SimpleNamespace(virtual_machine=None, status="Pending")
		with self.assertRaisesRegex(frappe.ValidationError, "Pending"):
			CargoServer._validate_provisioning_state(server)

	def test_virtual_machine_request_uses_the_reserved_public_address(self) -> None:
		server = SimpleNamespace(virtual_machine=None, status="Pending", failure_message=None)
		virtual_machine_service = MagicMock()
		virtual_machine_service.create.return_value = {"name": "vm-00001", "is_draft": False}
		with (
			patch.object(
				cargo_server_module.frappe,
				"get_single",
				return_value=SimpleNamespace(public_ssh_key="ssh-ed25519 AAAA atlas"),
			),
			patch("atlas.vm.core.vm_service.VirtualMachineService", virtual_machine_service),
		):
			is_draft = CargoServer._create_virtual_machine(
				server,
				{
					"virtual_machine_image": "image-1",
					"vcpus": 2,
					"memory_mib": 4096,
					"disk_mib": 16384,
					"server_ip_address": "203.0.113.9",
				},
			)

		request = virtual_machine_service.create.call_args.args[0]
		self.assertFalse(is_draft)
		self.assertEqual(server.virtual_machine, "vm-00001")
		self.assertEqual(request["tenant_id"], 0)
		self.assertTrue(request["is_privileged"])
		self.assertEqual(request["egress"], "uplink")
		self.assertEqual(request["hostname"], "cargo")
		self.assertEqual(request["server_ip_address"], "203.0.113.9")

	def test_a_busy_lifecycle_lock_refuses_the_request(self) -> None:
		with (
			patch.object(
				cargo_server_module,
				"filelock",
				side_effect=cargo_server_module.LockTimeoutError("busy"),
			),
			self.assertRaisesRegex(frappe.ValidationError, "lifecycle action"),
		):
			with cargo_server_module.cargo_lifecycle_lock():
				pass

	def test_pending_reconciliation_queues_the_same_virtual_machine(self) -> None:
		server = MagicMock(status="Pending", virtual_machine="vm-00001")
		with patch.object(cargo_server_module.frappe, "get_single", return_value=server):
			cargo_server_module.enqueue_pending_cargo_provisioning()

		server.enqueue_provisioning.assert_called_once_with(enqueue_after_commit=False)


class TestCargoServerArchive(UnitTestCase):
	def test_archive_removes_the_route_before_termination(self) -> None:
		events: list[str] = []
		server = MagicMock(status="Active", virtual_machine="vm-00001")
		virtual_machine = MagicMock()
		virtual_machine.terminate.side_effect = lambda: events.append("terminate")
		provisioner = MagicMock()
		provisioner.remove_proxy_routes.side_effect = lambda: events.append("routes")
		with (
			patch.object(cargo_server_module, "_validate_system_manager"),
			patch.object(cargo_server_module, "cargo_lifecycle_lock", return_value=nullcontext()),
			patch.object(cargo_server_module.frappe, "get_single", return_value=server),
			patch.object(cargo_server_module.frappe.db, "exists", return_value=True),
			patch.object(cargo_server_module.frappe, "get_doc", return_value=virtual_machine),
			patch("atlas.service.core.cargo.provisioning.CargoServerProvisioner", return_value=provisioner),
			patch.object(cargo_server_module.frappe, "msgprint"),
		):
			CargoServer.archive(server)

		self.assertEqual(events, ["routes", "terminate"])
		self.assertEqual(server.status, "Archived")
		self.assertIsNone(server.virtual_machine)
		self.assertIsNone(server.installation_task)

	def test_route_removal_failure_preserves_the_virtual_machine(self) -> None:
		server = MagicMock(status="Active", virtual_machine="vm-00001")
		provisioner = MagicMock()
		provisioner.remove_proxy_routes.side_effect = RuntimeError("proxy unavailable")
		with (
			patch.object(cargo_server_module, "_validate_system_manager"),
			patch.object(cargo_server_module, "cargo_lifecycle_lock", return_value=nullcontext()),
			patch.object(cargo_server_module.frappe, "get_single", return_value=server),
			patch("atlas.service.core.cargo.provisioning.CargoServerProvisioner", return_value=provisioner),
			patch.object(cargo_server_module.frappe.db, "commit"),
			self.assertRaises(RuntimeError),
		):
			CargoServer.archive(server)

		self.assertEqual(server.virtual_machine, "vm-00001")
		self.assertEqual(server.status, "Failed")
