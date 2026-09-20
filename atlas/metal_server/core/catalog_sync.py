from __future__ import annotations

import frappe

from atlas.atlas.core.server_providers.base import ServerImageData, ServerProvider, ServerSizeData


class CatalogSynchronizer:
	"""Store provider catalog data in Atlas records."""

	def __init__(self, provider: ServerProvider) -> None:
		self.provider = provider

	def sync_server_sizes(self) -> None:
		"""Store the current server sizes from the provider."""
		for size in self.provider.fetch_server_sizes():
			self.save_server_size(size)

	def sync_server_images(self) -> None:
		"""Store the current server images from the provider."""
		for image in self.provider.fetch_server_images():
			self.save_server_image(image)

	def save_server_size(self, size: ServerSizeData) -> None:
		"""Create or update one Metal Server Size record."""
		values = {
			"architecture": size.architecture,
			"cpu_count": size.cpu_count,
			"memory_mib": size.memory_mib,
			"disk_gib": size.disk_gib,
			"hourly_pricing_usd_cents": size.hourly_pricing_usd_cents,
			"monthly_pricing_usd_cents": size.monthly_pricing_usd_cents,
			"provider_metadata": frappe.as_json(size.provider_metadata),
		}
		if frappe.db.exists("Metal Server Size", size.name):
			document = frappe.get_doc("Metal Server Size", size.name)
			if all(document.get(field) == value for field, value in values.items()):
				return
			document.update(values)
			document.save(ignore_permissions=True)
			return

		frappe.get_doc(
			{
				"doctype": "Metal Server Size",
				"name": size.name,
				**values,
			}
		).insert(ignore_permissions=True)

	def save_server_image(self, image: ServerImageData) -> None:
		"""Create or update one Metal Server Image record."""
		values = {
			"os": image.os,
			"os_version": image.version,
			"provider_metadata": frappe.as_json(image.provider_metadata),
		}
		if frappe.db.exists("Metal Server Image", image.name):
			document = frappe.get_doc("Metal Server Image", image.name)
			if all(document.get(field) == value for field, value in values.items()):
				return
			document.update(values)
			document.save(ignore_permissions=True)
			return

		frappe.get_doc(
			{
				"doctype": "Metal Server Image",
				"name": image.name,
				**values,
			}
		).insert(ignore_permissions=True)
