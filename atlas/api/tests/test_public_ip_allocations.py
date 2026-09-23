from types import SimpleNamespace
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.api.models import PublicIPResponse
from atlas.api.routes.public_ips import (
	release_public_ip,
	reserve_attached_public_ip,
	reserve_public_ip,
)
from atlas.api.tests.test_support import TENANT_ID, api_request, call_route


def allocation(**values) -> SimpleNamespace:
	return SimpleNamespace(
		**(
			{
				"name": "32eb57bc-9548-4a89-8358-543e26883569",
				"tenant_id": TENANT_ID,
				"prefix": "203.0.113.10/32",
				"pool": "pool-1",
				"version": "4",
				"status": "Reserved",
				"is_reserved": 1,
				"virtual_machine": None,
				"creation": "2026-09-08T10:00:00+05:30",
				"reserve": Mock(),
				"release_reservation": Mock(),
			}
			| values
		)
	)


class TestPublicIPResponse(UnitTestCase):
	def test_response_derives_direct_delivery_from_the_pool(self) -> None:
		with patch("atlas.api.models.frappe.db.get_value", return_value=None):
			response = PublicIPResponse.from_document(allocation(), {})

		self.assertEqual(response.prefix, "203.0.113.10/32")
		self.assertEqual(response.delivery, "direct")
		self.assertEqual(response.status, "reserved")

	def test_response_derives_routed_delivery_from_the_pool(self) -> None:
		with patch("atlas.api.models.frappe.db.get_value", return_value="ipv6-router-001"):
			response = PublicIPResponse.from_document(allocation(prefix="2001:db8::1/128", version="6"), {})

		self.assertEqual(response.delivery, "routed")


class TestPublicIPRoutes(UnitTestCase):
	def test_reserve_selects_the_requested_version(self) -> None:
		reserved = allocation()
		with (
			api_request(
				"POST",
				"/api/atlas/public-ips",
				tenant_id=TENANT_ID,
				json={"version": 4},
			),
			patch(
				"atlas.api.routes.public_ips.PublicIPService.reserve",
				return_value=reserved,
			) as reserve,
			patch("atlas.api.models.frappe.db.get_value", return_value=None),
		):
			status, body = call_route(reserve_public_ip)

		self.assertEqual(status, 201)
		self.assertEqual(body["id"], reserved.name)
		reserve.assert_called_once_with(TENANT_ID, 4)

	def test_keep_sets_the_reservation_flag(self) -> None:
		owned = allocation()
		with (
			api_request(
				"PUT",
				f"/api/atlas/public-ips/{owned.name}/reserve",
				tenant_id=TENANT_ID,
			),
			patch(
				"atlas.api.routes.public_ips.get_owned_public_ip",
				return_value=owned,
			),
			patch("atlas.api.models.frappe.db.get_value", return_value=None),
		):
			status, _ = call_route(reserve_attached_public_ip, public_ip_id=owned.name)

		self.assertEqual(status, 200)
		owned.reserve.assert_called_once_with()

	def test_release_returns_no_content(self) -> None:
		owned = allocation()
		with (
			api_request(
				"DELETE",
				f"/api/atlas/public-ips/{owned.name}",
				tenant_id=TENANT_ID,
			),
			patch(
				"atlas.api.routes.public_ips.get_owned_public_ip",
				return_value=owned,
			),
		):
			status, body = call_route(release_public_ip, public_ip_id=owned.name)

		self.assertEqual(status, 204)
		self.assertIsNone(body)
		owned.release_reservation.assert_called_once_with()
