# Copyright (c) 2026, Frappe and Contributors
# See license.txt

import frappe
from frappe.tests import IntegrationTestCase

# On IntegrationTestCase, the doctype test records and all
# link-field test record dependencies are recursively loaded
# Use these module variables to add/remove to/from that list
EXTRA_TEST_RECORD_DEPENDENCIES = []  # eg. ["User"]
IGNORE_TEST_RECORD_DEPENDENCIES = []  # eg. ["User"]


class IntegrationTestVirtualMachineMigration(IntegrationTestCase):
	def test_migration_uses_explicit_metal_server_field_names(self) -> None:
		meta = frappe.get_meta("Virtual Machine Migration")
		fieldnames = {field.fieldname for field in meta.fields}

		self.assertTrue(
			{
				"source_metal_server",
				"destination_metal_server",
				"destination_metal_server_selection_attempts",
			}.issubset(fieldnames)
		)
		self.assertTrue(
			{"source_server", "target_server", "target_selection_attempts", "transferred_mib"}.isdisjoint(
				fieldnames
			)
		)

	def test_source_metal_server_does_not_follow_the_virtual_machine(self) -> None:
		"""The cutover moves the VM, so a fetched value would break set_only_once."""
		field = frappe.get_meta("Virtual Machine Migration").get_field("source_metal_server")

		self.assertTrue(field.set_only_once)
		self.assertFalse(field.fetch_from)

	def test_transfer_history_uses_a_child_table(self) -> None:
		migration_meta = frappe.get_meta("Virtual Machine Migration")
		transfer_field = migration_meta.get_field("transfers")
		transfer_meta = frappe.get_meta("Virtual Machine Migration Transfer")

		self.assertEqual(transfer_field.fieldtype, "Table")
		self.assertEqual(transfer_field.options, transfer_meta.name)
		self.assertTrue(transfer_meta.istable)

	def test_migration_fields_are_read_only(self) -> None:
		meta = frappe.get_meta("Virtual Machine Migration")
		value_fields = [
			field
			for field in meta.fields
			if field.fieldtype not in {"Column Break", "Section Break", "Tab Break"}
		]

		self.assertTrue(value_fields)
		self.assertTrue(all(field.read_only for field in value_fields))
