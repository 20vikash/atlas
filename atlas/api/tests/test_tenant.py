from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

from atlas.api.core.errors import InvalidRequest
from atlas.api.tests.test_support import api_request
from atlas.auth.request import validate_auth
from atlas.auth.roles import has_role
from atlas.auth.tenant import (
	MAXIMUM_TENANT_ID,
	TENANT_HEADER,
	get_tenant_id,
	parse_tenant_id,
)


class TestTenantHeader(UnitTestCase):
	def test_accepted_tenant_values(self) -> None:
		self.assertEqual(parse_tenant_id("1"), 1)
		self.assertEqual(parse_tenant_id(" 42 "), 42)
		self.assertEqual(parse_tenant_id(str(MAXIMUM_TENANT_ID)), MAXIMUM_TENANT_ID)

	def test_rejected_tenant_values(self) -> None:
		for value in ("", "   ", "0", "abc", "1.5", "-1", str(MAXIMUM_TENANT_ID + 1)):
			with self.assertRaises(InvalidRequest):
				parse_tenant_id(value)

	def test_failure_names_the_header(self) -> None:
		with self.assertRaises(InvalidRequest) as failure:
			parse_tenant_id("abc")

		self.assertEqual(failure.exception.fields[0]["name"], TENANT_HEADER)
		self.assertEqual(failure.exception.http_status_code, 400)

	def test_header_is_read_from_the_request(self) -> None:
		with api_request(tenant_id=9):
			self.assertEqual(get_tenant_id(), 9)

	def test_missing_header_is_rejected(self) -> None:
		with api_request(), self.assertRaises(InvalidRequest):
			get_tenant_id()

	def test_tenant_is_unknown_without_a_request(self) -> None:
		previous_request = getattr(frappe.local, "request", None)
		frappe.local.request = None
		try:
			with self.assertRaises(InvalidRequest):
				get_tenant_id()
		finally:
			frappe.local.request = previous_request


class TestAuthValidator(UnitTestCase):
	def test_non_atlas_user_is_left_to_frappe(self) -> None:
		with api_request(path="/api/resource/User"), patch("atlas.auth.request.has_role", return_value=False):
			validate_auth()

	def test_atlas_admin_can_use_only_atlas_routes(self) -> None:
		def has_atlas_role(role: str, user: str | None = None) -> bool:
			return role == "Atlas Admin"

		with (
			api_request(path="/api/atlas/images"),
			patch("atlas.auth.request.has_role", side_effect=has_atlas_role),
		):
			validate_auth()

		with (
			api_request(path="/api/resource/User"),
			patch("atlas.auth.request.has_role", side_effect=has_atlas_role),
			self.assertRaises(frappe.PermissionError),
		):
			validate_auth()

	def test_system_manager_can_use_any_route(self) -> None:
		def has_system_manager_role(role: str, user: str | None = None) -> bool:
			return role == "System Manager"

		with (
			api_request(path="/api/resource/User"),
			patch("atlas.auth.request.has_role", side_effect=has_system_manager_role),
		):
			validate_auth()
