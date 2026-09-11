from types import SimpleNamespace
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.auth import overrides


class TestMigrationPermissions(UnitTestCase):
	"""Migration read access follows read access to the linked VM."""

	def test_query_condition_scopes_to_the_tenant_vms(self) -> None:
		with patch.object(overrides, "_request_tenant_id", return_value=7):
			condition = overrides.get_permission_query_conditions(doctype="Virtual Machine Migration")

		self.assertIn("`tabVirtual Machine Migration`.`virtual_machine` in", condition)
		self.assertIn("`tenant_id` = 7", condition)

	def test_system_manager_reads_every_migration(self) -> None:
		with (
			patch.object(overrides, "_request_tenant_id", return_value=None),
			patch.object(overrides, "current_identity", return_value=None),
			patch.object(overrides, "has_role", return_value=True),
		):
			self.assertEqual(
				overrides.get_permission_query_conditions(doctype="Virtual Machine Migration"), ""
			)

	def test_read_follows_the_linked_virtual_machine(self) -> None:
		document = SimpleNamespace(doctype="Virtual Machine Migration", virtual_machine="vm-00001")
		frappe_permission = Mock(return_value=True)

		with (
			patch.object(overrides, "_request_tenant_id", return_value=7),
			patch.object(overrides.frappe, "has_permission", frappe_permission),
		):
			allowed = overrides.has_permission(document, "read")

		self.assertTrue(allowed)
		frappe_permission.assert_called_once_with(
			"Virtual Machine", ptype="read", doc="vm-00001", user=None
		)

	def test_write_is_denied_to_a_tenant(self) -> None:
		document = SimpleNamespace(doctype="Virtual Machine Migration", virtual_machine="vm-00001")

		with (
			patch.object(overrides, "_request_tenant_id", return_value=7),
			patch.object(overrides.frappe, "has_permission", return_value=True),
		):
			self.assertFalse(overrides.has_permission(document, "write"))
