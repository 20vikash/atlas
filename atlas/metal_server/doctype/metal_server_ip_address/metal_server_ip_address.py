from __future__ import annotations

import ipaddress
from dataclasses import dataclass

import frappe
from frappe import _
from frappe.model.document import Document

from atlas.atlas.core.background_jobs import run_as_admin
from atlas.atlas.core.tags import validate_tags
from atlas.metal_server.core.ip_address_service import UNOWNED_TENANT_ID, IPAddressService


@dataclass(frozen=True, slots=True)
class IPAddressIntent:
	"""Store one provider operation and its Atlas intent version."""

	version: int
	status: str
	provider_resource_id: str
	server: str | None
	reserved: bool


class MetalServerIPAddress(Document):
	"""One public IPv4 address owned by a server."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		from atlas.atlas.doctype.atlas_tag.atlas_tag import AtlasTag

		address: DF.Data
		intent_version: DF.Int
		provider_resource_id: DF.Data
		reserved: DF.Check
		server: DF.Link | None
		status: DF.Literal["Allocated", "Attaching", "Attached", "Detaching"]
		tags: DF.Table[AtlasTag]
		tenant_id: DF.Int
		virtual_machine: DF.Link | None
	# end: auto-generated types

	def validate(self) -> None:
		"""Validate the address and default it to the shared pool."""
		validate_tags(self)

		try:
			address = ipaddress.ip_interface(self.address)
		except ValueError:
			frappe.throw(_("IPv4 Address must be a valid /32 address."))
			return

		if not isinstance(address, ipaddress.IPv4Interface) or address.network.prefixlen != 32:
			frappe.throw(_("IPv4 Address must be a valid /32 address."))
		self.address = str(address.ip)

		# An unset Int becomes tenant 0, so set the shared-pool sentinel explicitly.
		if self.get("tenant_id") is None:
			self.tenant_id = UNOWNED_TENANT_ID

		if self.get("reserved") and self.tenant_id == UNOWNED_TENANT_ID:
			frappe.throw(_("A reserved IP address needs a tenant."))

	def on_trash(self) -> None:
		"""Release the provider reservation before the record is removed."""
		if self.status != "Allocated":
			frappe.throw(_("Detach this IP address before deletion."))
		frappe.get_single("Atlas Settings").server_provider_controller.delete_public_ipv4_address(
			self.provider_resource_id
		)

	def begin_assignment(self, server: str, virtual_machine: str) -> None:
		"""Set an attach intent for this address."""
		if self.status != "Allocated":
			frappe.throw(_("Metal Server IP Address {0} is not available.").format(self.name))

		self.status = "Attaching"
		self.server = server
		self.virtual_machine = virtual_machine
		self.intent_version = (self.intent_version or 0) + 1
		self.save()
		self.queue_reconcile()

	def release(self) -> None:
		"""Set a detach intent and return unreserved addresses to the pool."""
		self.virtual_machine = None
		self.intent_version = (self.intent_version or 0) + 1

		if self.server:
			self.status = "Detaching"
		else:
			self.status = "Allocated"
			if not self.reserved:
				self.tenant_id = UNOWNED_TENANT_ID
		self.save()
		self.queue_reconcile()

	@frappe.whitelist(methods=["POST"])
	def reserve(self) -> None:
		"""Keep this address with its tenant after detach."""
		self.check_permission("write")
		IPAddressService().reserve_held(self)

	def release_to_pool(self) -> None:
		"""Release this unused address to the shared pool."""
		self.check_permission("write")
		IPAddressService().release(self)

	@frappe.whitelist(methods=["POST"])
	def reset_tenant(self) -> None:
		"""Drop tenant ownership of this unattached address from the desk."""
		frappe.only_for("System Manager")
		if self.tenant_id == UNOWNED_TENANT_ID:
			frappe.throw(_("IP address {0} is already in the shared pool.").format(self.address))

		previous_tenant_id = self.tenant_id
		self.release_to_pool()
		self.add_comment(
			"Info",
			_("Reset to the shared pool from tenant {0}.").format(previous_tenant_id),
		)

	def queue_reconcile(self) -> None:
		"""Queue the provider reconcile job for this address."""
		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"reconcile",
			queue="default",
			job_id=f"reconcile-server-ip-{self.name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)

	@run_as_admin
	def reconcile(self) -> None:
		"""Apply the current provider intent."""
		current = frappe.get_doc(self.doctype, self.name)
		intent = current.get_intent()
		if intent.status not in {"Attaching", "Detaching"}:
			return

		try:
			self.apply_intent(intent)
			self.complete_intent(intent)
		except Exception:
			frappe.log_error(
				message=frappe.get_traceback(),
				title=(f"Metal Server IP Address {self.name} {intent.status} intent {intent.version} failed"),
			)
			raise

	def get_intent(self) -> IPAddressIntent:
		"""Return the desired provider state for this address."""
		return IPAddressIntent(
			version=self.intent_version or 0,
			status=self.status,
			provider_resource_id=self.provider_resource_id,
			server=self.server,
			reserved=bool(self.reserved),
		)

	def apply_intent(self, intent: IPAddressIntent) -> None:
		"""Make the provider state match the desired state."""
		provider = frappe.get_single("Atlas Settings").server_provider_controller
		if intent.status == "Attaching":
			if not intent.server:
				raise ValueError("An attach intent needs a Server")
			provider.attach_public_ipv4_address(
				intent.provider_resource_id, frappe.get_doc("Metal Server", intent.server)
			)
		else:
			provider.detach_public_ipv4_address(intent.provider_resource_id)

	def complete_intent(self, intent: IPAddressIntent) -> None:
		"""Complete an intent only when no newer intent exists."""
		table = frappe.qb.DocType("Metal Server IP Address")
		is_attaching = intent.status == "Attaching"

		query = (
			frappe.qb.update(table)
			.set(table.status, "Attached" if is_attaching else "Allocated")
			.set(table.server, intent.server if is_attaching else None)
			.where(table.name == self.name)
			.where(table.intent_version == intent.version)
		)
		if not is_attaching and not intent.reserved:
			query = query.set(table.tenant_id, UNOWNED_TENANT_ID)
		query.run()


def enqueue_pending_ip_address_reconcilation() -> None:
	"""Queue a reconcile job for each pending intent."""
	for name in frappe.get_all(
		"Metal Server IP Address",
		filters={"status": ["in", ["Attaching", "Detaching"]]},
		pluck="name",
	):
		frappe.get_doc("Metal Server IP Address", name).queue_reconcile()


@frappe.whitelist(methods=["POST"])
def reserve_for_pool() -> str:
	"""Add one provider reservation to the shared pool."""
	frappe.only_for("System Manager")
	return IPAddressService().reserve_from_provider()


def reserve_for_tenant(tenant_id: int) -> str:
	"""Reserve one shared pool address for a tenant."""
	if not frappe.has_permission("Metal Server IP Address", ptype="create"):
		raise frappe.PermissionError
	return IPAddressService().reserve(tenant_id)
