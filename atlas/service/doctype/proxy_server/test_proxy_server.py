# Copyright (c) 2026, Frappe and Contributors
# See license.txt

from unittest.mock import patch

import frappe
from frappe.tests import IntegrationTestCase


class IntegrationTestProxyServer(IntegrationTestCase):
	def build(self, **values) -> "frappe.Document":
		"""Insert one Proxy Server without running its setup job."""
		proxy_server = frappe.get_doc(
			{
				"doctype": "Proxy Server",
				"virtual_machine_image": "test-image",
				"vcpus": 2,
				"memory_mib": 4096,
				"disk_mib": 16384,
				"tenant_id": 0,
			}
			| values
		)
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

	def test_a_shape_without_capacity_is_refused(self) -> None:
		with self.assertRaises(frappe.ValidationError):
			self.build(vcpus=0)

	def test_setup_is_queued_when_the_record_is_created(self) -> None:
		proxy_server = frappe.get_doc(
			{
				"doctype": "Proxy Server",
				"virtual_machine_image": "test-image",
				"vcpus": 2,
				"memory_mib": 4096,
				"disk_mib": 16384,
				"tenant_id": 0,
			}
		)
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
		proxy_server = self.build()

		with self.assertRaises(frappe.ValidationError):
			proxy_server.validate_is_reachable()
