import frappe
from frappe.tests import IntegrationTestCase


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
