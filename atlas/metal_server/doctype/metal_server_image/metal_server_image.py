# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

import frappe
from frappe import _
from frappe.model.document import Document


class MetalServerImage(Document):
	"""One provider operating system image in the catalog."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		enabled: DF.Check
		image: DF.Data
		provider_metadata: DF.Code | None
		provider_type: DF.Literal["Scaleway"]
	# end: auto-generated types

	def autoname(self) -> None:
		"""Name the image from its provider and identifier."""
		if not self.provider_type or not self.image:
			frappe.throw(_("Metal Server Image requires provider_type and image"))
		self.name = f"{self.provider_type}/{self.image}"

	def validate(self) -> None:
		"""Reject an image without a provider identifier."""
		expected = f"{self.provider_type}/{self.image}"
		if self.name and self.name != expected:
			frappe.throw(_("Metal Server Image name {0} does not match {1}").format(self.name, expected))

	def get_provider_metadata(self, key: str) -> str:
		"""Return the provider values this image was created from."""
		metadata = frappe.parse_json(self.provider_metadata or "{}")
		value = metadata.get(key)
		if not isinstance(value, str):
			frappe.throw(_("Metal Server Image provider metadata has no {0}").format(key))
		return value
