from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

from atlas.api.core.errors import InvalidRequest
from atlas.api.tests.test_support import api_request
from atlas.auth.overrides import get_permission_query_conditions, has_permission
from atlas.auth.request import validate_auth
from atlas.auth.roles import has_role
from atlas.auth.tenant import (
	MAXIMUM_TENANT_ID,
	TENANT_HEADER,
	get_tenant_id,
	parse_tenant_id,
)
from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage


def build_image(tenant_id: int, image_type: str) -> VirtualMachineImage:
	"""Return one image document that answers the tenant visibility rule."""
	image = VirtualMachineImage.__new__(VirtualMachineImage)
	image.doctype = "Virtual Machine Image"
	image.tenant_id = tenant_id
	image.image_type = image_type
	return image


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


class TestTenantDocumentPermissions(UnitTestCase):
	def test_has_role_reads_the_cached_roles(self) -> None:
		with patch("frappe.get_roles", return_value=["Atlas Admin"]) as get_roles:
			self.assertTrue(has_role("Atlas Admin", "atlas@example.com"))

		get_roles.assert_called_once_with("atlas@example.com")

	def test_profile_user_list_is_filtered_by_request_tenant(self) -> None:
		with (
			api_request(tenant_id=9),
			patch("atlas.auth.overrides.has_role", return_value=False),
		):
			condition = get_permission_query_conditions(doctype="Virtual Machine")

		self.assertEqual(condition, "`tabVirtual Machine`.`tenant_id` = 9")

	def test_profile_user_without_tenant_cannot_list_documents(self) -> None:
		with (
			api_request(),
			patch("atlas.auth.overrides.has_role", return_value=False),
		):
			condition = get_permission_query_conditions(doctype="Virtual Machine")

		self.assertEqual(condition, "1=0")

	def test_profile_user_cannot_read_another_tenant_document(self) -> None:
		document = frappe._dict(doctype="Virtual Machine", tenant_id=8)
		with (
			api_request(tenant_id=7),
			patch("atlas.auth.overrides.has_role", return_value=False),
		):
			allowed = has_permission(document, "read")

		self.assertFalse(allowed)

	def test_profile_user_can_read_but_cannot_change_a_system_image(self) -> None:
		document = build_image(tenant_id=0, image_type="System")
		with (
			api_request(tenant_id=7),
			patch("atlas.auth.overrides.has_role", return_value=False),
		):
			self.assertTrue(has_permission(document, "read"))
			self.assertFalse(has_permission(document, "write"))
			self.assertFalse(has_permission(document, "delete"))

	def test_system_images_are_in_the_image_permission_query(self) -> None:
		with (
			api_request(tenant_id=7),
			patch("atlas.auth.overrides.has_role", return_value=False),
		):
			condition = get_permission_query_conditions(doctype="Virtual Machine Image")

		self.assertIn("tenant_id` = 7", condition)
		self.assertIn("image_type` = 'System'", condition)

	def test_system_manager_bypasses_tenant_filter(self) -> None:
		with patch("atlas.auth.overrides.has_role", return_value=True):
			condition = get_permission_query_conditions(doctype="Virtual Machine")

		self.assertEqual(condition, "")

	def test_unhandled_doctype_is_denied(self) -> None:
		document = frappe._dict(doctype="Unmanaged Atlas Document", tenant_id=7)
		with patch("atlas.auth.overrides.has_role", return_value=False):
			self.assertEqual(get_permission_query_conditions(doctype=document.doctype), "1=0")
			self.assertFalse(has_permission(document, "read"))


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
