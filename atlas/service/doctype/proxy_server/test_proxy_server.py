# Copyright (c) 2026, Frappe and Contributors
# See license.txt

from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import frappe
from frappe.tests import IntegrationTestCase, UnitTestCase

import atlas.service.doctype.proxy_server.proxy_server as proxy_server_module


class IntegrationTestProxyServer(IntegrationTestCase):
	def build(self, **values) -> "frappe.Document":
		"""Insert one Proxy Server without running its setup job."""
		proxy_server = frappe.get_doc(
			{
				"doctype": "Proxy Server",
				"virtual_machine": "vm-00001",
			}
			| values
		)
		proxy_server.flags.created_by_proxy_server_api = True
		with patch.object(type(proxy_server), "enqueue_provisioning"):
			proxy_server.insert(ignore_permissions=True, ignore_links=True)
		return proxy_server

	def test_the_name_uses_the_proxy_series(self) -> None:
		proxy_server = self.build()

		self.assertRegex(proxy_server.name, r"^proxy-\d{3}$")

	# Atlas holds the password and sends the proxy only its hash.
	def test_a_control_api_password_is_generated_once(self) -> None:
		proxy_server = self.build()

		password = proxy_server.get_password("control_api_password")

		self.assertTrue(password)
		proxy_server.save(ignore_permissions=True)
		self.assertEqual(proxy_server.get_password("control_api_password"), password)

	def test_creation_outside_the_api_is_refused(self) -> None:
		with self.assertRaises(frappe.ValidationError):
			frappe.get_doc({"doctype": "Proxy Server", "virtual_machine": "vm-00001"}).insert(
				ignore_permissions=True, ignore_links=True
			)

	def test_setup_is_queued_when_the_record_is_created(self) -> None:
		proxy_server = frappe.get_doc(
			{
				"doctype": "Proxy Server",
				"virtual_machine": "vm-00001",
			}
		)
		proxy_server.flags.created_by_proxy_server_api = True
		with patch.object(type(proxy_server), "enqueue_provisioning") as enqueue_provisioning:
			proxy_server.insert(ignore_permissions=True, ignore_links=True)

		enqueue_provisioning.assert_called_once()

	def test_deletion_is_refused_while_the_machine_exists(self) -> None:
		proxy_server = self.build()
		proxy_server.db_set("virtual_machine", "vm-00001")
		proxy_server.reload()

		with patch.object(frappe.db, "exists", return_value=True):
			with self.assertRaises(frappe.ValidationError):
				proxy_server.delete()

	def test_deletion_is_allowed_once_the_machine_is_gone(self) -> None:
		proxy_server = self.build()

		proxy_server.delete()

		self.assertFalse(frappe.db.exists("Proxy Server", proxy_server.name))

	def test_an_action_without_a_machine_is_refused(self) -> None:
		proxy_server = self.build(virtual_machine=None)

		with self.assertRaises(frappe.ValidationError):
			proxy_server.validate_is_reachable()

	def test_reprovision_queues_the_complete_setup_sequence(self) -> None:
		proxy_server = self.build(status="Failed")

		with (
			patch.object(proxy_server_module, "_validate_system_manager"),
			patch.object(proxy_server, "enqueue_provisioning") as enqueue_provisioning,
			patch.object(frappe, "msgprint"),
		):
			proxy_server.provision()

		enqueue_provisioning.assert_called_once()

	def test_a_system_manager_can_read_the_control_api_password(self) -> None:
		proxy_server = self.build()

		with (
			patch.object(proxy_server_module, "_validate_system_manager"),
			patch.object(proxy_server, "get_password", return_value="control-password"),
		):
			password = proxy_server.get_control_api_password()

		self.assertEqual(password, "control-password")

	def test_a_website_user_cannot_manage_proxies(self) -> None:
		with (
			patch.object(frappe, "only_for"),
			patch.object(frappe, "get_cached_value", return_value="Website User"),
			self.assertRaises(frappe.PermissionError),
		):
			proxy_server_module._validate_system_manager()


class TestProxyServerCreate(UnitTestCase):
	def test_the_creation_api_creates_a_proxy_for_the_new_vm(self) -> None:
		proxy_server = MagicMock(name="proxy_server")
		proxy_server.name = "proxy-001"
		proxy_server.flags = SimpleNamespace()
		virtual_machine_service = MagicMock()
		virtual_machine_service.create.return_value = {"name": "vm-00001", "is_draft": False}

		with (
			patch.object(proxy_server_module.frappe, "only_for"),
			patch.object(proxy_server_module.frappe, "new_doc", return_value=proxy_server),
			patch.object(
				proxy_server_module.frappe,
				"get_single",
				return_value=SimpleNamespace(public_ssh_key="ssh-ed25519 AAAA atlas"),
			),
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.reserve",
				return_value="203.0.113.9",
			),
			patch("atlas.vm.core.vm_service.VirtualMachineService", virtual_machine_service),
		):
			name = proxy_server_module.create(
				{
					"virtual_machine_image": "image-1",
					"vcpus": 2,
					"memory_mib": 4096,
					"disk_mib": 16384,
				}
			)

		self.assertEqual(name, {"name": "proxy-001", "is_draft": False})
		self.assertEqual(proxy_server.virtual_machine, "vm-00001")
		self.assertEqual(virtual_machine_service.create.call_args.args[0]["tenant_id"], 0)
		self.assertTrue(virtual_machine_service.create.call_args.args[0]["is_privileged"])
		self.assertEqual(virtual_machine_service.create.call_args.args[0]["hostname"], "proxy-001")
		proxy_server.enqueue_provisioning.assert_called_once()

	def test_pending_proxies_are_queued(self) -> None:
		with (
			patch.object(proxy_server_module.frappe, "get_all", return_value=["proxy-001"]),
			patch.object(proxy_server_module.frappe, "enqueue_doc") as enqueue_doc,
		):
			proxy_server_module.enqueue_pending_proxies_provisioning()

		enqueue_doc.assert_called_once_with(
			"Proxy Server",
			"proxy-001",
			"_provision",
			queue="long",
			timeout=3_600,
			job_id="atlas||proxy-server||provision||proxy-001",
			deduplicate=True,
			enqueue_after_commit=False,
		)
