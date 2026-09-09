from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.metal_server.core.ip_address_service import (
	UNOWNED_TENANT_ID,
	IPAddressPoolEmpty,
	IPAddressService,
)

TENANT_ID = 7


class TestIPAddressReservation(UnitTestCase):
	def test_an_unknown_source_is_rejected(self) -> None:
		with self.assertRaises(frappe.ValidationError):
			IPAddressService().reserve(TENANT_ID, "somewhere")

	def test_an_empty_pool_does_not_reach_the_provider(self) -> None:
		service = IPAddressService()

		with (
			patch.object(service, "get_pool_candidates", return_value=[]),
			patch.object(service, "reserve_from_provider") as reserve_from_provider,
			self.assertRaises(IPAddressPoolEmpty),
		):
			service.reserve(TENANT_ID, "pool")

		reserve_from_provider.assert_not_called()

	def test_a_claimed_candidate_is_skipped(self) -> None:
		"""A concurrent claim locks the row first, so the next candidate is used."""
		service = IPAddressService()
		database = Mock(get_value=Mock(side_effect=[None, "203.0.113.11"]), set_value=Mock())

		with (
			patch.object(service, "get_pool_candidates", return_value=["203.0.113.10", "203.0.113.11"]),
			patch(
				"atlas.metal_server.core.ip_address_service.frappe.db",
				database,
			),
		):
			self.assertEqual(service.reserve(TENANT_ID, "pool"), "203.0.113.11")

		self.assertTrue(database.get_value.call_args_list[0].kwargs["for_update"])
		database.set_value.assert_called_once_with(
			"Metal Server IP Address", "203.0.113.11", "tenant_id", TENANT_ID
		)

	def test_a_failed_insert_returns_the_provider_reservation(self) -> None:
		provider = Mock()
		provider.reserve_public_ipv4_address.return_value = SimpleNamespace(
			address="203.0.113.10", provider_resource_id="provider-1"
		)

		with (
			patch(
				"atlas.metal_server.core.ip_address_service.frappe.get_single",
				return_value=SimpleNamespace(server_provider_controller=provider),
			),
			patch(
				"atlas.metal_server.core.ip_address_service.frappe.get_doc",
				side_effect=RuntimeError("insert failed"),
			),
			self.assertRaises(RuntimeError),
		):
			IPAddressService().reserve_from_provider(TENANT_ID)

		provider.delete_public_ipv4_address.assert_called_once_with("provider-1")


class TestIPAddressRelease(UnitTestCase):
	def test_release_returns_the_address_to_the_pool(self) -> None:
		ip_address = SimpleNamespace(name="203.0.113.10")
		locked_address = SimpleNamespace(status="Allocated", virtual_machine=None, db_set=Mock())

		with patch(
			"atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address
		) as get_doc:
			IPAddressService().release(ip_address)

		get_doc.assert_called_once_with("Metal Server IP Address", "203.0.113.10", for_update=True)
		locked_address.db_set.assert_called_once_with("tenant_id", UNOWNED_TENANT_ID)

	def test_an_attached_address_cannot_be_released(self) -> None:
		for values in (
			{"status": "Attached", "virtual_machine": "vm-1"},
			{"status": "Detaching", "virtual_machine": None},
			{"status": "Allocated", "virtual_machine": "vm-1"},
		):
			ip_address = SimpleNamespace(name="203.0.113.10")
			locked_address = SimpleNamespace(db_set=Mock(), **values)

			with (
				patch(
					"atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address
				),
				self.assertRaises(frappe.ValidationError),
			):
				IPAddressService().release(ip_address)

			locked_address.db_set.assert_not_called()
