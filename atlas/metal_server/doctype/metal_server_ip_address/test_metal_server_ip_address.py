from contextlib import contextmanager
from dataclasses import FrozenInstanceError
from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.server_providers.base import ReservedIPAddress
from atlas.metal_server.core.ip_address_service import UNOWNED_TENANT_ID
from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import (
	IPAddressIntent,
	MetalServerIPAddress,
)


@contextmanager
def provider_with_prefix_length(prefix_length: int | None):
	"""Run with a provider that gives one IPv6 prefix length, or none."""

	class Provider:
		public_ipv6_prefix_length = prefix_length

	with patch(
		"frappe.get_single",
		return_value=SimpleNamespace(server_provider="Scaleway", server_provider_controller=Provider()),
	):
		yield


class TestServerIPAddress(UnitTestCase):
	def test_reserved_ip_address_is_frozen(self) -> None:
		reserved = ReservedIPAddress("203.0.113.10", "provider-id")

		with self.assertRaises(FrozenInstanceError):
			reserved.address = "203.0.113.11"

	def test_an_address_without_a_tenant_goes_to_the_shared_pool(self) -> None:
		"""An unset tenant uses the shared-pool sentinel."""
		address = frappe.get_doc(
			{
				"doctype": "Metal Server IP Address",
				"address": "203.0.113.10",
				"provider_resource_id": "provider-1",
			}
		)

		with patch("frappe.get_single", return_value=SimpleNamespace(server_provider="AWS")):
			address.validate()

		self.assertEqual(address.tenant_id, UNOWNED_TENANT_ID)
		self.assertEqual(address.provider_resource_id, "provider-1")

	def test_generic_address_is_its_own_provider_resource_id(self) -> None:
		address = frappe.get_doc(
			{
				"doctype": "Metal Server IP Address",
				"address": "203.0.113.10",
				"provider_resource_id": "incorrect-resource-id",
			}
		)

		with patch("frappe.get_single", return_value=SimpleNamespace(server_provider="Generic")):
			address.validate()

		self.assertEqual(address.provider_resource_id, "203.0.113.10")

	def test_reset_tenant_returns_the_address_to_the_pool(self) -> None:
		address = SimpleNamespace(
			address="203.0.113.10", tenant_id=7, release_to_pool=Mock(), add_comment=Mock()
		)

		with patch("frappe.only_for") as only_for:
			MetalServerIPAddress.reset_tenant(address)

		only_for.assert_called_once_with("System Manager")
		address.release_to_pool.assert_called_once()

	def test_reset_tenant_records_the_losing_tenant(self) -> None:
		address = SimpleNamespace(
			address="203.0.113.10", tenant_id=7, release_to_pool=Mock(), add_comment=Mock()
		)

		with patch("frappe.only_for"):
			MetalServerIPAddress.reset_tenant(address)

		address.add_comment.assert_called_once()
		kind, message = address.add_comment.call_args.args
		self.assertEqual(kind, "Info")
		self.assertIn("7", message)

	def test_reset_tenant_needs_a_system_manager(self) -> None:
		address = SimpleNamespace(
			address="203.0.113.10", tenant_id=7, release_to_pool=Mock(), add_comment=Mock()
		)

		with patch("frappe.only_for", side_effect=frappe.PermissionError):
			with self.assertRaises(frappe.PermissionError):
				MetalServerIPAddress.reset_tenant(address)

		address.release_to_pool.assert_not_called()

	def test_an_unowned_address_is_already_in_the_pool(self) -> None:
		address = SimpleNamespace(
			address="203.0.113.10", tenant_id=-1, release_to_pool=Mock(), add_comment=Mock()
		)

		with patch("frappe.only_for"), self.assertRaises(frappe.ValidationError):
			MetalServerIPAddress.reset_tenant(address)

		address.release_to_pool.assert_not_called()

	def test_assignment_increments_the_intent_version(self) -> None:
		address = SimpleNamespace(
			name="203.0.113.10",
			is_ipv6=False,
			status="Allocated",
			server=None,
			virtual_machine=None,
			host_address=None,
			intent_version=4,
			save=Mock(),
			queue_reconcile=Mock(),
		)

		MetalServerIPAddress.begin_assignment(address, SimpleNamespace(name="node-1"), "VM-00001")

		self.assertEqual(address.intent_version, 5)
		self.assertEqual(address.status, "Attaching")
		address.queue_reconcile.assert_called_once()

	def test_an_unreserved_address_leaves_the_tenant_on_release(self) -> None:
		address = SimpleNamespace(
			name="203.0.113.10",
			status="Attached",
			server=None,
			virtual_machine="VM-00001",
			host_address=None,
			tenant_id=7,
			reserved=0,
			intent_version=1,
			save=Mock(),
			queue_reconcile=Mock(),
		)

		MetalServerIPAddress.release(address)

		self.assertEqual(address.status, "Allocated")
		self.assertEqual(address.tenant_id, UNOWNED_TENANT_ID)

	def test_a_reserved_address_keeps_its_tenant_on_release(self) -> None:
		address = SimpleNamespace(
			name="203.0.113.10",
			status="Attached",
			server=None,
			virtual_machine="VM-00001",
			host_address=None,
			tenant_id=7,
			reserved=1,
			intent_version=1,
			save=Mock(),
			queue_reconcile=Mock(),
		)

		MetalServerIPAddress.release(address)

		self.assertEqual(address.status, "Allocated")
		self.assertEqual(address.tenant_id, 7)

	def test_a_detach_returns_an_unreserved_address_to_the_pool(self) -> None:
		for reserved, expects_pool_return in ((False, True), (True, False)):
			intent = IPAddressIntent(
				version=3,
				status="Detaching",
				provider_resource_id="provider-id",
				server="node-1",
				reserved=reserved,
				public_address="203.0.113.10",
				host_address="10.1.8.55",
				virtual_machine=None,
				is_ipv6=False,
			)
			query = Mock()
			query.set.return_value = query
			query.where.return_value = query
			query_builder = Mock(DocType=Mock(), update=Mock(return_value=query))

			with (
				patch(
					"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.qb",
					new=query_builder,
				),
				patch(
					"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.db.get_value",
					return_value=None,
				),
			):
				MetalServerIPAddress.complete_intent(
					SimpleNamespace(doctype="Metal Server IP Address", name="203.0.113.10"),
					intent,
					None,
				)

			tenant_writes = [call for call in query.set.call_args_list if call.args[1] == UNOWNED_TENANT_ID]
			self.assertEqual(bool(tenant_writes), expects_pool_return)

	def test_reconcile_applies_one_intent_per_job(self) -> None:
		intent = IPAddressIntent(
			1,
			"Attaching",
			"provider-id",
			"node-1",
			True,
			"203.0.113.10",
			None,
			"VM-00001",
			False,
		)
		worker = SimpleNamespace(
			doctype="Metal Server IP Address",
			name="203.0.113.10",
			apply_intent=Mock(return_value="10.1.8.55"),
			complete_intent=Mock(return_value=True),
			send_address_to_metal=Mock(),
		)

		with patch(
			"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_doc",
			return_value=SimpleNamespace(get_intent=Mock(return_value=intent)),
		):
			MetalServerIPAddress.reconcile(worker)

		worker.apply_intent.assert_called_once_with(intent)
		worker.complete_intent.assert_called_once_with(intent, worker.apply_intent.return_value)
		worker.send_address_to_metal.assert_called_once_with("VM-00001", "10.1.8.55")

	def test_reconcile_does_not_send_a_stale_intent_to_metal(self) -> None:
		intent = IPAddressIntent(
			1,
			"Attaching",
			"provider-id",
			"node-1",
			True,
			"203.0.113.10",
			None,
			"VM-00001",
			False,
		)
		worker = SimpleNamespace(
			doctype="Metal Server IP Address",
			name="203.0.113.10",
			apply_intent=Mock(return_value="10.1.8.55"),
			complete_intent=Mock(return_value=False),
			send_address_to_metal=Mock(),
		)

		with patch(
			"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_doc",
			return_value=SimpleNamespace(get_intent=Mock(return_value=intent)),
		):
			MetalServerIPAddress.reconcile(worker)

		worker.send_address_to_metal.assert_not_called()

	def test_reconcile_logs_the_resource_intent_and_version(self) -> None:
		intent = IPAddressIntent(
			7,
			"Attaching",
			"provider-id",
			"node-1",
			True,
			"203.0.113.10",
			None,
			"VM-00001",
			False,
		)
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

	def test_ipv6_prefix_is_stored_as_its_network_address(self) -> None:
		for text in ("2001:db8:1:2::/64", "2001:db8:1:2::"):
			with self.subTest(text=text):
				address = self.ipv6_address(text)

				with provider_with_prefix_length(64):
					# The autoname field takes its value before validation runs.
					address.before_naming()

				self.assertEqual(address.address, "2001:db8:1:2::")
				self.assertEqual(address.cidr, 64)

	def test_ipv6_uses_the_prefix_length_of_the_provider(self) -> None:
		address = self.ipv6_address("2001:db8:1:2::")

		with provider_with_prefix_length(80):
			address.before_naming()

		self.assertEqual(address.address, "2001:db8:1:2::")
		self.assertEqual(address.cidr, 80)

	def test_ipv6_refuses_another_prefix_length(self) -> None:
		address = self.ipv6_address("2001:db8:1:2::/64")

		with provider_with_prefix_length(80), self.assertRaisesRegex(frappe.ValidationError, "/80"):
			address.before_naming()

	def test_ipv6_takes_the_record_cidr_without_a_provider_length(self) -> None:
		address = self.ipv6_address("2001:db8:1::")
		address.cidr = 56

		with provider_with_prefix_length(None):
			address.before_naming()

		self.assertEqual(address.cidr, 56)

	def test_ipv6_needs_a_cidr_without_a_provider_length(self) -> None:
		address = self.ipv6_address("2001:db8:1:2::")

		with provider_with_prefix_length(None), self.assertRaisesRegex(frappe.ValidationError, "CIDR"):
			address.before_naming()

	def test_ipv6_needs_a_network_address(self) -> None:
		for text in ("2001:db8:1:2::5", "203.0.113.10"):
			with self.subTest(text=text):
				address = self.ipv6_address(text)

				with provider_with_prefix_length(64), self.assertRaises(frappe.ValidationError):
					address.before_naming()

	def test_ipv4_always_uses_32(self) -> None:
		address = frappe.get_doc(
			{
				"doctype": "Metal Server IP Address",
				"address": "203.0.113.10",
				"provider_resource_id": "provider-1",
			}
		)

		address.before_naming()

		self.assertEqual(address.cidr, 32)

	@staticmethod
	def ipv6_address(text: str) -> MetalServerIPAddress:
		return frappe.get_doc(
			{
				"doctype": "Metal Server IP Address",
				"version": "6",
				"address": text,
				"provider_resource_id": "fip-1",
			}
		)

	def test_ipv6_attach_returns_no_host_address(self) -> None:
		intent = IPAddressIntent(
			1, "Attaching", "fip-1", "node-1", False, "2001:db8:1:2::", None, None, is_ipv6=True
		)
		provider = Mock()
		provider.attach_public_ip_address.return_value = "2001:db8:1:2::"

		with (
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_single",
				return_value=SimpleNamespace(server_provider_controller=provider),
			),
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_doc"
			),
		):
			host_address = MetalServerIPAddress.apply_intent(SimpleNamespace(), intent)

		self.assertIsNone(host_address)
		provider.attach_public_ip_address.assert_called_once()

	def test_attach_needs_an_ipv4_host_address(self) -> None:
		intent = IPAddressIntent(
			1,
			"Attaching",
			"provider-id",
			"node-1",
			True,
			"203.0.113.10",
			None,
			"VM-00001",
			False,
		)
		provider = Mock()
		provider.attach_public_ip_address.return_value = "not-an-address"

		with (
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_single",
				return_value=SimpleNamespace(server_provider_controller=provider),
			),
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.frappe.get_doc"
			),
			self.assertRaisesRegex(ValueError, "IPv4 host address"),
		):
			MetalServerIPAddress.apply_intent(SimpleNamespace(), intent)

	def test_a_move_detaches_now_and_attaches_on_the_new_server(self) -> None:
		provider = Mock()
		address = SimpleNamespace(
			provider_resource_id="fip-1",
			host_address="10.0.0.9",
			server="node-1",
			status="Attached",
			intent_version=4,
			save=Mock(),
			queue_reconcile=Mock(),
		)

		with (
			patch("frappe.get_single", return_value=SimpleNamespace(server_provider_controller=provider)),
			patch("frappe.get_doc", return_value="node-1-document"),
		):
			MetalServerIPAddress.move_to_server(address, "node-2")

		provider.detach_public_ip_address.assert_called_once_with("fip-1", "10.0.0.9", "node-1-document")
		self.assertEqual(
			(address.status, address.server, address.host_address, address.intent_version),
			("Attaching", "node-2", None, 5),
		)
		address.queue_reconcile.assert_called_once()

	def test_an_ipv4_block_is_refused(self) -> None:
		for values in ({"address": "203.0.113.0/24"}, {"address": "203.0.113.0", "cidr": 24}):
			address = frappe.get_doc({"doctype": "Metal Server IP Address", "version": "4", **values})

			with self.assertRaisesRegex(frappe.ValidationError, "IPv4 blocks are not supported"):
				address.get_ipv4_address()
