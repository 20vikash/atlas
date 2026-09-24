import frappe
from frappe.tests import IntegrationTestCase

from atlas.metal_server.core.public_ip_service import PublicIPService, generate_available_allocations


def insert_pool(prefix: str, allocation_prefix_length: int, **values):
	return frappe.get_doc(
		{
			"doctype": "Public IP Pool",
			"prefix": prefix,
			"allocation_prefix_length": allocation_prefix_length,
			"source": "Static",
			**values,
		}
	).insert(ignore_permissions=True)


class TestPublicIPPool(IntegrationTestCase):
	def test_ipv4_pool_is_canonical_and_allocates_32_bit_prefixes(self) -> None:
		pool = insert_pool("198.19.10.7/24", 28)

		self.assertEqual(pool.prefix, "198.19.10.0/24")
		self.assertEqual(pool.version, "4")
		self.assertEqual(pool.allocation_prefix_length, 32)
		self.assertEqual(pool.provider_status, "Not Applicable")

	def test_ipv6_pool_keeps_its_allocation_size(self) -> None:
		pool = insert_pool("2001:db8:10::1/64", 80)

		self.assertEqual(pool.prefix, "2001:db8:10::/64")
		self.assertEqual(pool.version, "6")
		self.assertEqual(pool.allocation_prefix_length, 80)

	def test_overlapping_pool_is_refused(self) -> None:
		insert_pool("198.19.20.0/24", 32)

		with self.assertRaisesRegex(frappe.ValidationError, "overlaps"):
			insert_pool("198.19.20.128/25", 32)

	def test_static_pool_refuses_a_provider_resource(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "static pool"):
			insert_pool("198.19.30.0/24", 32, provider_resource_id="resource-1")

	def test_provider_pool_cannot_be_divided_without_a_gateway(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "cannot be divided"):
			frappe.get_doc(
				{
					"doctype": "Public IP Pool",
					"prefix": "2001:db8:30::/64",
					"allocation_prefix_length": 80,
					"source": "Provider",
					"provider_resource_id": "resource-2",
				}
			).insert(ignore_permissions=True)

	def test_provider_pool_generates_its_allocation_on_demand(self) -> None:
		pool = frappe.get_doc(
			{
				"doctype": "Public IP Pool",
				"prefix": "198.19.30.1/32",
				"allocation_prefix_length": 32,
				"source": "Provider",
				"provider_resource_id": "resource-3",
			}
		).insert(ignore_permissions=True)

		with self.assertRaisesRegex(ValueError, "when an address is requested"):
			generate_available_allocations(pool.name)

		self.assertFalse(frappe.db.exists("Public IP Allocation", {"pool": pool.name}))

	def test_delete_removes_available_allocations(self) -> None:
		pool = insert_pool("198.19.31.0/30", 32)
		generate_available_allocations(pool.name)

		frappe.delete_doc("Public IP Pool", pool.name, ignore_permissions=True)

		self.assertFalse(frappe.db.exists("Public IP Allocation", {"pool": pool.name}))

	def test_delete_refuses_allocations_in_use(self) -> None:
		pool = insert_pool("198.19.32.0/32", 32)
		allocation = PublicIPService()._create_next(pool)
		allocation.status = "Reserved"
		allocation.tenant_id = 7
		allocation.is_reserved = 1
		allocation.save(ignore_permissions=True)

		with self.assertRaisesRegex(frappe.ValidationError, "allocations in use"):
			frappe.delete_doc("Public IP Pool", pool.name, ignore_permissions=True)

		self.assertTrue(frappe.db.exists("Public IP Pool", pool.name))
		self.assertTrue(frappe.db.exists("Public IP Allocation", allocation.name))

	def test_delete_refuses_a_pool_assigned_to_an_ipv6_router(self) -> None:
		pool = insert_pool("2001:db8:32::/64", 128)
		pool.db_set("gateway", f"ipv6-router-{pool.name}", update_modified=False)
		pool.reload()

		with self.assertRaisesRegex(frappe.ValidationError, "Archive.*IPv6 Router Server"):
			frappe.delete_doc("Public IP Pool", pool.name, ignore_permissions=True)

		self.assertTrue(frappe.db.exists("Public IP Pool", pool.name))
