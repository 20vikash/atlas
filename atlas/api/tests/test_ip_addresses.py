from types import SimpleNamespace
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.api.models import IPAddressResponse
from atlas.api.routes.ip_addresses import (
	get_ip_address,
	list_ip_addresses,
	release_ip_address,
	reserve_ip_address,
)
from atlas.api.tests.test_support import TENANT_ID, api_request, call_route


def build_ip_address(tenant_id: int = TENANT_ID, **overrides) -> SimpleNamespace:
	"""Return one stored public IPv4 address row."""
	values = {
		"name": "203.0.113.10",
		"tenant_id": tenant_id,
		"address": "203.0.113.10",
		"status": "Allocated",
		"reserved": 1,
		"virtual_machine": None,
		"creation": "2026-09-08 10:00:00",
		"provider_resource_id": "provider-1",
		"server": "node-1",
	}
	values.update(overrides)
	return SimpleNamespace(**values)


def owned_document(ip_address: SimpleNamespace):
	"""Patch the ownership lookup so it returns one IP address."""
	return patch("atlas.api.routes.ip_addresses.get_owned_document", return_value=ip_address)


class TestIPAddressView(UnitTestCase):
	def test_view_maps_the_status_and_hides_provider_data(self) -> None:
		view = IPAddressResponse.from_document(build_ip_address(status="Attached", virtual_machine="vm-1"))

		self.assertEqual(view.state, "attached")
		self.assertTrue(view.reserved)
		self.assertEqual(view.virtual_machine_id, "vm-1")
		self.assertIsInstance(view.created_at, int)
		self.assertNotIn("provider_resource_id", view.model_fields)
		self.assertNotIn("server", view.model_fields)


class TestReserveIPAddress(UnitTestCase):
	def reserve(self, body: dict):
		"""Run the reserve route against one request body."""
		with (
			api_request("POST", "/api/atlas/ip-addresses", tenant_id=TENANT_ID, json=body),
			patch(
				"atlas.api.routes.ip_addresses.reserve_for_tenant",
				return_value="203.0.113.10",
			) as reserve,
			patch("atlas.api.routes.ip_addresses.frappe.get_doc", return_value=build_ip_address()),
		):
			return (*call_route(reserve_ip_address), reserve)

	def test_an_empty_body_claims_one_pool_address(self) -> None:
		status, body, reserve = self.reserve({})

		self.assertEqual(status, 201)
		self.assertEqual(body["state"], "reserved")
		reserve.assert_called_once_with(TENANT_ID)

	def test_a_named_address_is_reserved_where_it_is(self) -> None:
		ip_address = build_ip_address(status="Attached", virtual_machine="vm-1", reserved=0)
		ip_address.reserve = Mock()

		with (
			api_request(
				"POST",
				"/api/atlas/ip-addresses",
				tenant_id=TENANT_ID,
				json={"ip_address_id": "203.0.113.10"},
			),
			owned_document(ip_address),
			patch("atlas.api.routes.ip_addresses.reserve_for_tenant") as reserve,
		):
			status, body = call_route(reserve_ip_address)

		self.assertEqual(status, 200)
		self.assertEqual(body["virtual_machine_id"], "vm-1")
		ip_address.reserve.assert_called_once()
		reserve.assert_not_called()

	def test_an_unknown_field_is_rejected(self) -> None:
		status, _, _ = self.reserve({"source": "provider"})

		self.assertEqual(status, 400)


class TestReadIPAddresses(UnitTestCase):
	def test_list_filters_by_tenant(self) -> None:
		with (
			api_request("GET", "/api/atlas/ip-addresses", tenant_id=TENANT_ID),
			patch(
				"atlas.api.routes.ip_addresses.frappe.get_list", return_value=[build_ip_address()]
			) as get_list,
			patch("atlas.api.routes.ip_addresses.read_tags_for", return_value={"203.0.113.10": {}}),
		):
			status, body = call_route(list_ip_addresses)

		self.assertEqual(status, 200)
		self.assertEqual(get_list.call_args.kwargs["filters"], {"tenant_id": TENANT_ID})
		self.assertFalse(body["has_more"])


class TestReleaseIPAddress(UnitTestCase):
	def release(self, ip_address: SimpleNamespace):
		"""Run the release route against one stored address."""
		ip_address.release_to_pool = Mock()
		with (
			api_request("DELETE", "/api/atlas/ip-addresses/203.0.113.10", tenant_id=TENANT_ID),
			owned_document(ip_address),
		):
			return (*call_route(release_ip_address, ip_address_id="203.0.113.10"), ip_address.release_to_pool)

	def test_release_returns_no_content(self) -> None:
		status, body, release = self.release(build_ip_address())

		self.assertEqual(status, 204)
		self.assertIsNone(body)
		release.assert_called_once()

	def test_an_attached_address_returns_a_conflict(self) -> None:
		from atlas.metal_server.core.ip_address_service import IPAddressInUse

		ip_address = build_ip_address(status="Attached", virtual_machine="vm-1")
		ip_address.release_to_pool = Mock(side_effect=IPAddressInUse("Address is in use."))
		with (
			api_request("DELETE", "/api/atlas/ip-addresses/203.0.113.10", tenant_id=TENANT_ID),
			owned_document(ip_address),
		):
			status, body = call_route(release_ip_address, ip_address_id="203.0.113.10")

		self.assertEqual(status, 409)
		self.assertEqual(body["error"]["code"], "conflict")
