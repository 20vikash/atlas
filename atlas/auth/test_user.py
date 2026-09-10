from unittest.mock import patch

import frappe
from frappe.tests import IntegrationTestCase

from atlas.auth.user import ATLAS_ADMIN_ROLE, ensure_tenant_user

TENANT_ID = 4242


class TestTenantUser(IntegrationTestCase):
	def tearDown(self) -> None:
		name = f"tenant-{TENANT_ID}@atlas.local"
		if frappe.db.exists("User", name):
			frappe.delete_doc("User", name, force=True, ignore_permissions=True)
			frappe.db.commit()  # nosemgrep

	def test_a_tenant_user_is_created_once(self) -> None:
		name = ensure_tenant_user(TENANT_ID)

		self.assertEqual(name, f"tenant-{TENANT_ID}@atlas.local")
		self.assertIn(ATLAS_ADMIN_ROLE, frappe.get_roles(name))

		with patch("atlas.auth.user._insert_api_user") as insert:
			self.assertEqual(ensure_tenant_user(TENANT_ID), name)

		insert.assert_not_called()

	def test_a_concurrent_first_request_does_not_fail(self) -> None:
		with patch(
			"atlas.auth.user._insert_api_user",
			side_effect=frappe.DuplicateEntryError("User", f"tenant-{TENANT_ID}@atlas.local"),
		):
			self.assertEqual(ensure_tenant_user(TENANT_ID), f"tenant-{TENANT_ID}@atlas.local")
