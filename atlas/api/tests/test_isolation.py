import frappe
from frappe.tests import IntegrationTestCase

from atlas.api.core.base import get_owned_document
from atlas.api.core.errors import ResourceNotFound
from atlas.api.routes.images import get_image
from atlas.api.routes.public_ips import (
	get_public_ip,
	list_public_ips,
)
from atlas.api.tests.test_images import insert_image
from atlas.api.tests.test_support import OTHER_TENANT_ID, TENANT_ID, api_request, call_route


def insert_public_ip_allocation(tenant_id: int) -> str:
	"""Insert one static pool and one reserved allocation."""
	index = frappe.db.count("Public IP Pool") + 20
	prefix = f"198.18.{index // 256}.{index % 256}/32"
	pool = frappe.get_doc(
		{
			"doctype": "Public IP Pool",
			"prefix": prefix,
			"allocation_prefix_length": 32,
			"source": "Static",
			"enabled": 1,
		}
	).insert(ignore_permissions=True)
	allocation = frappe.get_doc(
		{
			"doctype": "Public IP Allocation",
			"prefix": prefix,
			"pool": pool.name,
			"version": "4",
			"status": "Reserved",
			"tenant_id": tenant_id,
			"is_reserved": 1,
		}
	)
	allocation.flags.created_by_public_ip_allocator = True
	return allocation.insert(ignore_permissions=True).name


class TestOwnedDocument(IntegrationTestCase):
	"""get_owned_document carries the tenant rule for every single-resource route."""

	def setUp(self) -> None:
		self.own_image = insert_image(TENANT_ID)
		self.other_image = insert_image(OTHER_TENANT_ID)

	def test_the_request_tenant_reads_its_own_document(self) -> None:
		with api_request(tenant_id=TENANT_ID):
			document = get_owned_document("Virtual Machine Image", self.own_image)

		self.assertEqual(document.name, self.own_image)

	def test_another_tenant_document_is_absent(self) -> None:
		with api_request(tenant_id=OTHER_TENANT_ID), self.assertRaises(ResourceNotFound):
			get_owned_document("Virtual Machine Image", self.own_image)

	def test_an_empty_name_is_absent(self) -> None:
		with api_request(tenant_id=TENANT_ID), self.assertRaises(ResourceNotFound) as failure:
			get_owned_document("Virtual Machine Image", "", "image")

		self.assertEqual(str(failure.exception), "The image does not exist.")

	def test_a_central_request_without_a_tenant_header_is_invalid(self) -> None:
		with api_request():
			status, body = call_route(get_image, image_id=self.own_image)

		self.assertEqual(status, 400)
		self.assertEqual(body["error"]["code"], "invalid_request")


class TestPermissionQueryConditions(IntegrationTestCase):
	"""A list query is tenant scoped by the Frappe permission hook, without a route filter."""

	def setUp(self) -> None:
		self.own_image = insert_image(TENANT_ID)
		self.other_image = insert_image(OTHER_TENANT_ID)
		self.system_image = insert_image(0, "system")

	def test_the_hook_filters_a_query_that_carries_no_tenant_filter(self) -> None:
		with api_request(tenant_id=TENANT_ID):
			names = {row.name for row in frappe.get_list("Virtual Machine Image", limit=100)}

		self.assertIn(self.own_image, names)
		self.assertIn(self.system_image, names)
		self.assertNotIn(self.other_image, names)

	def test_a_query_without_an_identity_or_a_role_is_refused(self) -> None:
		previous_user = frappe.session.user
		frappe.set_user("Guest")
		try:
			with self.assertRaises(frappe.PermissionError):
				frappe.get_list("Virtual Machine Image", limit=100)
		finally:
			frappe.set_user(previous_user)


class TestPublicIPAllocationIsolation(IntegrationTestCase):
	def setUp(self) -> None:
		self.own_allocation = insert_public_ip_allocation(TENANT_ID)
		self.other_allocation = insert_public_ip_allocation(OTHER_TENANT_ID)

	def test_a_list_holds_only_the_tenant_addresses(self) -> None:
		with api_request(
			"GET",
			"/api/atlas/public-ips",
			tenant_id=TENANT_ID,
			query_string={"limit": "100"},
		):
			status, body = call_route(list_public_ips)

		self.assertEqual(status, 200)
		names = {item["id"] for item in body["items"]}
		self.assertIn(self.own_allocation, names)
		self.assertNotIn(self.other_allocation, names)

	def test_another_tenant_cannot_read_the_address(self) -> None:
		with api_request("GET", "/api/atlas/public-ips/x", tenant_id=OTHER_TENANT_ID):
			status, body = call_route(get_public_ip, public_ip_id=self.own_allocation)

		self.assertEqual(status, 404)
		self.assertEqual(body["error"]["code"], "public_ip_not_found")
