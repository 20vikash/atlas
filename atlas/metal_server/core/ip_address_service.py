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
UNOWNED_TENANT_ID = -1


class IPAddressPoolEmpty(AtlasUserError):
	"""Report that the shared pool holds no free address."""

	http_status_code = 409


class IPAddressInUse(AtlasUserError):
	"""Report that an address cannot change its reservation."""

	http_status_code = 409


class IPAddressService:
	"""Own tenant reservations for public IPv4 addresses."""

	def reserve(self, tenant_id: int) -> str:
		"""Reserve one shared pool address for a tenant."""
		address_name = self.borrow_from_pool()
		frappe.db.set_value("Metal Server IP Address", address_name, {"tenant_id": tenant_id, "reserved": 1})
		return address_name

	def borrow_from_pool(self) -> str:
		"""Lock one unused shared pool address. The caller decides whether it becomes a reservation."""
		for name in self.get_pool_candidates():
			locked = frappe.db.get_value(
				"Metal Server IP Address",
				{"name": name, "tenant_id": UNOWNED_TENANT_ID, "status": "Allocated"},
				"name",
				for_update=True,
			)
			if locked:
				return name

		frappe.throw(_("The shared IP address pool is empty."), exc=IPAddressPoolEmpty)

	def get_pool_candidates(self) -> list[str]:
		"""Return addresses that no tenant reserved and no virtual machine uses."""
		return frappe.get_all(
			"Metal Server IP Address",
			filters={
				"tenant_id": UNOWNED_TENANT_ID,
				"status": "Allocated",
				"virtual_machine": ["is", "not set"],
			},
			pluck="name",
			limit=POOL_SEARCH_LIMIT,
			order_by="creation asc",
		)

	def reserve_from_provider(self) -> str:
		"""Add one provider reservation to the shared pool."""
		provider = frappe.get_single("Atlas Settings").server_provider_controller
		reserved = provider.reserve_public_ipv4_address()
		try:
			ip_address: MetalServerIPAddress = frappe.get_doc(
				{
					"doctype": "Metal Server IP Address",
					"address": reserved.address,
					"provider_resource_id": reserved.provider_resource_id,
					"tenant_id": UNOWNED_TENANT_ID,
				}
			).insert()
			return ip_address.name
		except Exception:
			try:
				provider.delete_public_ipv4_address(reserved.provider_resource_id)
			except Exception:
				frappe.log_error(title="Could not delete reserved Metal Server IP Address")
			raise

	def reserve_held(self, ip_address: MetalServerIPAddress) -> None:
		"""Keep a held address with its tenant after detach."""
		locked_address = frappe.get_doc("Metal Server IP Address", ip_address.name, for_update=True)
		if locked_address.tenant_id == UNOWNED_TENANT_ID:
			frappe.throw(_("An IP address in the shared pool has no tenant."), exc=IPAddressInUse)
		# The caller authorized an unlocked read. The address can reach another tenant before this lock.
		if locked_address.tenant_id != ip_address.tenant_id:
			frappe.throw(_("Another tenant now holds this IP address."), exc=IPAddressInUse)
		if locked_address.status == "Detaching":
			frappe.throw(_("This IP address is on its way back to the shared pool."), exc=IPAddressInUse)

		locked_address.db_set("reserved", 1)
		ip_address.reserved = 1

	def release(self, ip_address: MetalServerIPAddress) -> None:
		"""Release an address to the shared pool."""
		locked_address = frappe.get_doc("Metal Server IP Address", ip_address.name, for_update=True)
		if locked_address.status != "Allocated" or locked_address.virtual_machine:
			frappe.throw(_("Detach this IP address before you release it."), exc=IPAddressInUse)
		locked_address.db_set({"tenant_id": UNOWNED_TENANT_ID, "reserved": 0})
		ip_address.tenant_id = UNOWNED_TENANT_ID
		ip_address.reserved = 0
