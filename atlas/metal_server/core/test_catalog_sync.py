from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.server_providers.base import ServerImageData, ServerSizeData
from atlas.metal_server.core.catalog_sync import CatalogSynchronizer


class TestCatalogSynchronizer(UnitTestCase):
	def test_sync_server_sizes_creates_a_provider_catalog_record(self) -> None:
		provider = SimpleNamespace(
			provider_type="Test",
			fetch_server_sizes=Mock(
				return_value=(
					ServerSizeData(
						name="large",
						architecture="amd64",
						cpu_count=4,
						memory_mib=8192,
						disk_gib=100,
						hourly_pricing_usd_cents=10,
						monthly_pricing_usd_cents=7200,
						provider_metadata={"id": "offer-id"},
					),
				)
			),
		)
		document = Mock()
		exists = Mock(return_value=False)

		with (
			patch("atlas.metal_server.core.catalog_sync.frappe.db", SimpleNamespace(exists=exists)),
			patch("atlas.metal_server.core.catalog_sync.frappe.get_doc", return_value=document) as get_doc,
		):
			CatalogSynchronizer(provider).sync_server_sizes()

		# One Atlas site serves one provider, so the size identifier is the record name.
		exists.assert_called_once_with("Metal Server Size", "large")
		values = get_doc.call_args.args[0]
		# The identifier is the record name, so the size carries no separate field.
		self.assertEqual(values["name"], "large")
		self.assertEqual(values["architecture"], "amd64")
		# One Atlas site serves one provider, so the catalog does not record one.
		self.assertNotIn("provider_type", values)
		self.assertNotIn("size", values)
		self.assertEqual(values["memory_mib"], 8192)
		document.insert.assert_called_once_with(ignore_permissions=True)

	def test_sync_server_images_skips_unchanged_metadata(self) -> None:
		provider = SimpleNamespace(
			provider_type="Test",
			fetch_server_images=Mock(
				return_value=(
					ServerImageData(
						name="Ubuntu_26.04",
						os="Ubuntu",
						version="26.04",
						provider_metadata={"id": "image-id"},
					),
				)
			),
		)
		document = Mock()
		document.get.side_effect = {
			"os": "Ubuntu",
			"os_version": "26.04",
			"provider_metadata": frappe.as_json({"id": "image-id"}),
		}.get
		exists = Mock(return_value=True)

		with (
			patch("atlas.metal_server.core.catalog_sync.frappe.db", SimpleNamespace(exists=exists)),
			patch("atlas.metal_server.core.catalog_sync.frappe.get_doc", return_value=document),
		):
			CatalogSynchronizer(provider).sync_server_images()

		exists.assert_called_once_with("Metal Server Image", "Ubuntu_26.04")
		document.save.assert_not_called()

	def test_sync_server_images_stores_the_operating_system(self) -> None:
		provider = SimpleNamespace(
			provider_type="Test",
			fetch_server_images=Mock(
				return_value=(
					ServerImageData(
						name="Ubuntu_26.04",
						os="Ubuntu",
						version="26.04",
						provider_metadata={"id": "image-id"},
					),
				)
			),
		)
		document = Mock()

		with (
			patch(
				"atlas.metal_server.core.catalog_sync.frappe.db",
				SimpleNamespace(exists=Mock(return_value=False)),
			),
			patch("atlas.metal_server.core.catalog_sync.frappe.get_doc", return_value=document) as get_doc,
		):
			CatalogSynchronizer(provider).sync_server_images()

		values = get_doc.call_args.args[0]
		# The identifier is the record name, so the image carries no separate field.
		self.assertEqual(values["name"], "Ubuntu_26.04")
		self.assertEqual(values["os"], "Ubuntu")
		self.assertEqual(values["os_version"], "26.04")
		self.assertNotIn("provider_type", values)
		self.assertNotIn("image", values)
		document.insert.assert_called_once_with(ignore_permissions=True)
