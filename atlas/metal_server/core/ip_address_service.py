from __future__ import annotations

from typing import TYPE_CHECKING

import frappe
from frappe import _

from atlas.atlas.core.exceptions import AtlasUserError

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import (
		MetalServerIPAddress,
	)

POOL_SEARCH_LIMIT = 20
RESERVATION_SOURCES = ("pool", "provider")


class IPAddressPoolEmpty(AtlasUserError):
	"""Report that the shared pool holds no free address."""

	http_status_code = 409


class IPAddressInUse(AtlasUserError):
	"""Report that an address cannot return to the shared pool."""

	http_status_code = 409


class IPAddressService:
	"""Own tenant reservations for public IPv4 addresses."""

	def reserve(self, tenant_id: int, source: str) -> str:
		"""Reserve one address for a tenant from the pool or from the provider."""
		if source not in RESERVATION_SOURCES:
			frappe.throw(_("Reservation source must be pool or provider."))
		if source == "provider":
			return self.reserve_from_provider(tenant_id)

		address_name = self.claim_from_pool(tenant_id)
		if not address_name:
			frappe.throw(_("The shared IP address pool is empty."), exc=IPAddressPoolEmpty)
		return address_name

	def claim_from_pool(self, tenant_id: int) -> str | None:
		"""Claim one unowned address. A concurrent claim takes the next candidate."""
		for name in self.get_pool_candidates():
			locked = frappe.db.get_value(
				"Metal Server IP Address",
				{"name": name, "tenant_id": ["is", "not set"], "status": "Allocated"},
				"name",
				for_update=True,
			)
			if locked:
				frappe.db.set_value("Metal Server IP Address", name, "tenant_id", tenant_id)
				return name

		return None

	def get_pool_candidates(self) -> list[str]:
		"""Return addresses that no tenant reserved and no virtual machine uses."""
		return frappe.get_all(
			"Metal Server IP Address",
			filters={
				"tenant_id": ["is", "not set"],
				"status": "Allocated",
				"virtual_machine": ["is", "not set"],
			},
			pluck="name",
			limit=POOL_SEARCH_LIMIT,
			order_by="creation asc",
		)

	def reserve_from_provider(self, tenant_id: int | None) -> str:
		"""Create one provider reservation and assign its tenant."""
		provider = frappe.get_single("Atlas Settings").server_provider_controller
		reserved = provider.reserve_public_ipv4_address()
		try:
			ip_address: MetalServerIPAddress = frappe.get_doc(
				{
					"doctype": "Metal Server IP Address",
					"address": reserved.address,
					"provider_resource_id": reserved.provider_resource_id,
					"tenant_id": tenant_id,
				}
			).insert()
			return ip_address.name
		except Exception:
			try:
				provider.delete_public_ipv4_address(reserved.provider_resource_id)
			except Exception:
				frappe.log_error(title="Could not delete reserved Metal Server IP Address")
			raise

	def release(self, ip_address: MetalServerIPAddress) -> None:
		"""Return one unattached address to the shared pool and keep its reservation."""
		locked_address = frappe.get_doc("Metal Server IP Address", ip_address.name, for_update=True)
		if locked_address.status != "Allocated" or locked_address.virtual_machine:
			frappe.throw(_("Detach this IP address before you release it."), exc=IPAddressInUse)
		locked_address.db_set("tenant_id", None)
		ip_address.tenant_id = None
