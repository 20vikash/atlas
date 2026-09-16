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
	def test_an_empty_pool_does_not_reach_the_provider(self) -> None:
		service = IPAddressService()

		with (
			patch.object(service, "get_pool_candidates", return_value=[]),
			patch.object(service, "reserve_from_provider") as reserve_from_provider,
			self.assertRaises(IPAddressPoolEmpty),
		):
			service.reserve(TENANT_ID)

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
			self.assertEqual(service.reserve(TENANT_ID), "203.0.113.11")

		self.assertTrue(database.get_value.call_args_list[0].kwargs["for_update"])
		database.set_value.assert_called_once_with(
			"Metal Server IP Address", "203.0.113.11", {"tenant_id": TENANT_ID, "reserved": 1}
		)

	def test_a_borrow_locks_a_pool_address_without_a_write(self) -> None:
		service = IPAddressService()
		database = Mock(get_value=Mock(return_value="203.0.113.10"), set_value=Mock())

		with (
			patch.object(service, "get_pool_candidates", return_value=["203.0.113.10"]),
			patch("atlas.metal_server.core.ip_address_service.frappe.db", database),
		):
			self.assertEqual(service.borrow_from_pool(), "203.0.113.10")

		self.assertTrue(database.get_value.call_args.kwargs["for_update"])
		database.set_value.assert_not_called()

	def test_an_empty_pool_refuses_a_borrow(self) -> None:
		service = IPAddressService()

		with (
			patch.object(service, "get_pool_candidates", return_value=[]),
			self.assertRaises(IPAddressPoolEmpty),
		):
			service.borrow_from_pool()

	def test_a_provider_reservation_lands_in_the_shared_pool(self) -> None:
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
				return_value=Mock(insert=Mock(return_value=SimpleNamespace(name="203.0.113.10"))),
			) as get_doc,
		):
			IPAddressService().reserve_from_provider()

		self.assertEqual(get_doc.call_args.args[0]["tenant_id"], UNOWNED_TENANT_ID)

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
			IPAddressService().reserve_from_provider()

		provider.delete_public_ipv4_address.assert_called_once_with("provider-1")


class TestHeldIPAddressReservation(UnitTestCase):
	def test_an_attached_address_can_be_reserved(self) -> None:
		"""An attached borrowed address can be kept."""
		ip_address = SimpleNamespace(name="203.0.113.10", tenant_id=TENANT_ID, reserved=0)
		locked_address = SimpleNamespace(tenant_id=TENANT_ID, status="Attached", db_set=Mock())

		with patch(
			"atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address
		) as get_doc:
			IPAddressService().reserve_held(ip_address)

		get_doc.assert_called_once_with("Metal Server IP Address", "203.0.113.10", for_update=True)
		locked_address.db_set.assert_called_once_with("reserved", 1)
		self.assertEqual(ip_address.reserved, 1)

	def test_an_address_that_changed_tenant_cannot_be_reserved(self) -> None:
		"""Ownership was read without a lock, so the row can reach another tenant first."""
		ip_address = SimpleNamespace(name="203.0.113.10", tenant_id=TENANT_ID, reserved=0)
		locked_address = SimpleNamespace(tenant_id=TENANT_ID + 1, status="Attached", db_set=Mock())

		with (
			patch("atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address),
			self.assertRaises(frappe.ValidationError),
		):
			IPAddressService().reserve_held(ip_address)

		locked_address.db_set.assert_not_called()

	def test_a_detaching_address_cannot_be_reserved(self) -> None:
		"""A detaching address cannot be kept."""
		ip_address = SimpleNamespace(name="203.0.113.10", tenant_id=TENANT_ID, reserved=0)
		locked_address = SimpleNamespace(tenant_id=TENANT_ID, status="Detaching", db_set=Mock())

		with (
			patch("atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address),
			self.assertRaises(frappe.ValidationError),
		):
			IPAddressService().reserve_held(ip_address)

		locked_address.db_set.assert_not_called()

	def test_a_pool_address_has_no_tenant_to_reserve_it_for(self) -> None:
		ip_address = SimpleNamespace(name="203.0.113.10", tenant_id=TENANT_ID, reserved=0)
		locked_address = SimpleNamespace(tenant_id=UNOWNED_TENANT_ID, status="Allocated", db_set=Mock())

		with (
			patch("atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address),
			self.assertRaises(frappe.ValidationError),
		):
			IPAddressService().reserve_held(ip_address)

		locked_address.db_set.assert_not_called()


class TestIPAddressRelease(UnitTestCase):
	def test_release_returns_the_address_to_the_pool(self) -> None:
		ip_address = SimpleNamespace(name="203.0.113.10")
		locked_address = SimpleNamespace(status="Allocated", virtual_machine=None, db_set=Mock())

		with patch(
			"atlas.metal_server.core.ip_address_service.frappe.get_doc", return_value=locked_address
		) as get_doc:
			IPAddressService().release(ip_address)

		get_doc.assert_called_once_with("Metal Server IP Address", "203.0.113.10", for_update=True)
		locked_address.db_set.assert_called_once_with({"tenant_id": UNOWNED_TENANT_ID, "reserved": 0})

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
