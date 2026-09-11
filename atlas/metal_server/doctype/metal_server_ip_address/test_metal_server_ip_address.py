from dataclasses import FrozenInstanceError
from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.server_providers.base import ReservedIPAddress
from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import (
	IPAddressIntent,
	MetalServerIPAddress,
)


class TestServerIPAddress(UnitTestCase):
	def test_reserved_ip_address_is_frozen(self) -> None:
		reserved = ReservedIPAddress("203.0.113.10", "provider-id")

		with self.assertRaises(FrozenInstanceError):
			reserved.address = "203.0.113.11"

	def test_reset_tenant_returns_the_address_to_the_pool(self) -> None:
		address = SimpleNamespace(address="203.0.113.10", tenant_id=7, release_to_pool=Mock())

		with patch("frappe.only_for") as only_for:
			MetalServerIPAddress.reset_tenant(address)

		only_for.assert_called_once_with("System Manager")
		address.release_to_pool.assert_called_once()

	def test_reset_tenant_needs_a_system_manager(self) -> None:
		address = SimpleNamespace(address="203.0.113.10", tenant_id=7, release_to_pool=Mock())

		with patch("frappe.only_for", side_effect=frappe.PermissionError):
			with self.assertRaises(frappe.PermissionError):
				MetalServerIPAddress.reset_tenant(address)

		address.release_to_pool.assert_not_called()

	def test_an_unowned_address_is_already_in_the_pool(self) -> None:
		address = SimpleNamespace(address="203.0.113.10", tenant_id=-1, release_to_pool=Mock())

		with patch("frappe.only_for"), self.assertRaises(frappe.ValidationError):
			MetalServerIPAddress.reset_tenant(address)

		address.release_to_pool.assert_not_called()

	def test_assignment_increments_the_intent_version(self) -> None:
		address = SimpleNamespace(
			name="203.0.113.10",
			status="Allocated",
			server=None,
			virtual_machine=None,
			intent_version=4,
			save=Mock(),
			queue_reconcile=Mock(),
		)

		MetalServerIPAddress.begin_assignment(address, SimpleNamespace(name="node-1"), "VM-00001")

		self.assertEqual(address.intent_version, 5)
		self.assertEqual(address.status, "Attaching")
		address.queue_reconcile.assert_called_once()

	def test_reconcile_applies_one_intent_per_job(self) -> None:
		intent = IPAddressIntent(1, "Attaching", "provider-id", "node-1")
		worker = SimpleNamespace(
			doctype="Metal Server IP Address",
			name="203.0.113.10",
			apply_intent=Mock(),
			complete_intent=Mock(),
		)

		with patch(
			"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_doc",
			return_value=SimpleNamespace(get_intent=Mock(return_value=intent)),
		):
			MetalServerIPAddress.reconcile(worker)

		worker.apply_intent.assert_called_once_with(intent)
		worker.complete_intent.assert_called_once_with(intent)

	def test_reconcile_logs_the_resource_intent_and_version(self) -> None:
		intent = IPAddressIntent(7, "Attaching", "provider-id", "node-1")
		worker = SimpleNamespace(
			doctype="Metal Server IP Address",
			name="203.0.113.10",
			apply_intent=Mock(side_effect=RuntimeError("provider failed")),
			complete_intent=Mock(),
		)

		with (
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_doc",
				return_value=SimpleNamespace(get_intent=Mock(return_value=intent)),
			),
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.log_error"
			) as log_error,
			self.assertRaisesRegex(RuntimeError, "provider failed"),
		):
			MetalServerIPAddress.reconcile(worker)

		self.assertEqual(
			log_error.call_args.kwargs["title"],
			"Metal Server IP Address 203.0.113.10 Attaching intent 7 failed",
		)
		worker.complete_intent.assert_not_called()
