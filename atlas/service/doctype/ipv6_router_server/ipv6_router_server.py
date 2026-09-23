# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

from contextlib import contextmanager
from typing import TYPE_CHECKING, Any

import frappe
from frappe import _
from frappe.model.document import Document
from frappe.utils.file_lock import LockTimeoutError
from frappe.utils.synchronization import filelock

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.atlas.core.mesh_address import get_virtual_machine_mesh_address
from atlas.metal_server.core.ip_address_service import UNOWNED_TENANT_ID
from atlas.service.core.ipv6_router.address import (
	ROUTED_DESTINATION,
	get_routed_ipv6,
	parse_ipv6_network,
	validate_router_network,
)

if TYPE_CHECKING:
	from collections.abc import Iterator

	from atlas.vm.doctype.virtual_machine.virtual_machine import VirtualMachine


class IPv6RouterServer(Document):
	"""Own one IPv6 router, its virtual machine, and its public IPv6 block."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		failure_message: DF.SmallText | None
		installation_task: DF.Link | None
		ipv6_block: DF.Link
		server: DF.Link | None
		status: DF.Literal["Pending", "Provisioning", "Active", "Failed", "Archived"]
		virtual_machine: DF.Link | None
		wireguard_mesh_ipv6: DF.Data | None
	# end: auto-generated types

	@property
	def prefix(self) -> str:
		"""Return the public IPv6 block that the router translates."""
		return frappe.get_doc("Metal Server IP Address", self.ipv6_block).prefix

	def before_insert(self) -> None:
		"""Reject records created outside the IPv6 Router Server API."""
		if not getattr(self.flags, "created_by_ipv6_router_api", False):
			frappe.throw(_("Create IPv6 Router Servers from the IPv6 Router Server list."))

	def on_trash(self) -> None:
		"""Refuse deletion while the virtual machine exists."""
		if self.status != "Archived" and self.virtual_machine:
			frappe.throw(_("Archive IPv6 Router Server {0} before you delete it.").format(self.name))

	@staticmethod
	def create(request: str | dict[str, Any]) -> dict[str, str | bool]:
		"""Create an IPv6 Router Server and its virtual machine, then queue setup."""
		_validate_system_manager()
		values = frappe.parse_json(request) if isinstance(request, str) else request
		if not isinstance(values, dict):
			frappe.throw(_("IPv6 Router Server creation data must be an object."))
		_validate_create_request(values)

		router = frappe.new_doc("IPv6 Router Server")
		router.ipv6_block = values["ipv6_block"]
		router.flags.created_by_ipv6_router_api = True
		router.insert(ignore_permissions=True)
		frappe.db.commit()  # nosemgrep

		is_draft = router._create_virtual_machine(values)
		router.save(ignore_permissions=True)
		if router.status != "Failed":
			router.enqueue_provisioning()
		return {"name": router.name, "is_draft": is_draft}

	@frappe.whitelist(methods=["POST"])
	def archive(self) -> None:
		"""Terminate the router virtual machine. Termination releases its addresses."""
		_validate_system_manager()
		with ipv6_router_lifecycle_lock(self.name):
			router: IPv6RouterServer = frappe.get_doc(self.doctype, self.name)
			if router.status == "Archived":
				return

			try:
				if router.virtual_machine and frappe.db.exists("Virtual Machine", router.virtual_machine):
					virtual_machine = frappe.get_doc("Virtual Machine", router.virtual_machine)
					virtual_machine.set_termination_protection(False)
					virtual_machine.terminate()
			except Exception as error:
				router.status = "Failed"
				router.failure_message = f"archive: {error}"
				router.save(ignore_permissions=True)
				frappe.db.commit()  # nosemgrep
				raise

			router.status = "Archived"
			router.failure_message = None
			router.virtual_machine = None
			router.installation_task = None
			router.save(ignore_permissions=True)

		frappe.msgprint(_("IPv6 Router Server {0} is archived.").format(self.name))

	def attach_virtual_machine(self, virtual_machine: VirtualMachine) -> str:
		"""Send the Internet range of a VM through this router and return its public address."""
		from atlas.vm.core.vm_service import VirtualMachineService

		if self.status != "Active":
			frappe.throw(_("IPv6 Router Server {0} is not Active.").format(self.name), exc=AtlasUserError)

		address = self.get_public_address(virtual_machine)
		service = VirtualMachineService(virtual_machine)
		routes = [
			route for route in service.get_gateway_routes() if route["destination"] != ROUTED_DESTINATION
		]
		service.set_gateway_routes(
			[*routes, {"destination": ROUTED_DESTINATION, "gateway": self.wireguard_mesh_ipv6}]
		)
		return address

	def get_public_address(self, virtual_machine: VirtualMachine) -> str:
		"""Return the address in this router's block that maps to the VM."""
		return get_routed_ipv6(self.prefix, get_virtual_machine_mesh_address(virtual_machine))

	def enqueue_provisioning(self, enqueue_after_commit: bool = True) -> None:
		"""Queue router setup for the attached virtual machine."""
		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_provision",
			queue="long",
			timeout=3_600,
			job_id=f"atlas||ipv6-router-server||provision||{self.name}",
			deduplicate=True,
			enqueue_after_commit=enqueue_after_commit,
		)

	def _provision(self) -> None:
		from atlas.service.core.ipv6_router.provisioning import IPv6RouterServerProvisioner

		IPv6RouterServerProvisioner(self).run()

	def _create_virtual_machine(self, values: dict[str, Any]) -> bool:
		from atlas.vm.core.vm_service import VirtualMachineCreateError, VirtualMachineService

		request = {
			"virtual_machine_image": values.get("virtual_machine_image"),
			"cpu_millicores": values.get("cpu_millicores"),
			"memory_mib": values.get("memory_mib"),
			"disk_mib": values.get("disk_mib"),
			"tenant_id": 0,
			"is_privileged": True,
			"is_termination_protected": True,
			"hostname": self.name,
			"ssh_keys": frappe.get_single("Atlas Settings").public_ssh_key,
			"egress": "uplink",
			"server_ip_address": values["server_ip_address"],
		}
		try:
			result = VirtualMachineService.create(request)
			self._set_virtual_machine(result["name"])
			return bool(result["is_draft"])
		except VirtualMachineCreateError as error:
			self._set_virtual_machine(error.virtual_machine_name)
			self.status = "Failed"
			self.failure_message = f"virtual-machine: {error}"
			return True

	def _set_virtual_machine(self, name: str) -> None:
		"""Store the router VM and its stable mesh address."""
		self.virtual_machine = name
		self.wireguard_mesh_ipv6 = get_virtual_machine_mesh_address(frappe._dict(name=name, tenant_id=0))


@frappe.whitelist(methods=["POST"])
def create(request: str | dict[str, Any]) -> dict[str, str | bool]:
	"""Call the IPv6 Router Server creation service from the list view."""
	return IPv6RouterServer.create(request)


def _validate_create_request(values: dict[str, Any]) -> None:
	"""Reject a create request that cannot produce a working router."""
	from atlas.vm.core.models import VirtualMachineCreateRequest
	from atlas.vm.core.vm_service import VirtualMachineService

	virtual_machine_request = {
		"virtual_machine_image": values.get("virtual_machine_image"),
		"cpu_millicores": values.get("cpu_millicores"),
		"memory_mib": values.get("memory_mib"),
		"disk_mib": values.get("disk_mib"),
		"tenant_id": 0,
		"is_privileged": True,
		"is_termination_protected": True,
		"hostname": "ipv6-router",
		"ssh_keys": frappe.get_single("Atlas Settings").public_ssh_key,
		"egress": "uplink",
		"server_ip_address": values.get("server_ip_address"),
	}
	try:
		request = VirtualMachineCreateRequest.from_value(virtual_machine_request)
	except ValueError as error:
		frappe.throw(_(str(error)), exc=AtlasUserError)
		raise AssertionError from error

	image = VirtualMachineService.get_image(request.virtual_machine_image, request.tenant_id)
	if image.image_type != "system":
		frappe.throw(_("Select a System Virtual Machine Image."))
	image.validate_compatibility(request.disk_mib)

	_validate_ipv4_address(request.server_ip_address)
	_validate_ipv6_block(values.get("ipv6_block"))


def _validate_ipv4_address(address_name: object) -> None:
	"""Require a free tenant-0 or pool IPv4 address for the router VM."""
	if not isinstance(address_name, str) or not frappe.db.exists("Metal Server IP Address", address_name):
		frappe.throw(_("Select an allocated public IPv4 address."))

	address = frappe.get_doc("Metal Server IP Address", address_name)
	if (
		address.is_ipv6
		or address.status != "Allocated"
		or address.virtual_machine
		or address.tenant_id not in (UNOWNED_TENANT_ID, 0)
	):
		frappe.throw(_("Select an allocated public IPv4 address that no virtual machine holds."))


def _validate_ipv6_block(block_name: object) -> None:
	"""Require a free tenant-0 or pool IPv6 block with space for the tenant and VM fields."""
	if not isinstance(block_name, str) or not frappe.db.exists("Metal Server IP Address", block_name):
		frappe.throw(_("Select an allocated IPv6 block."))

	block = frappe.get_doc("Metal Server IP Address", block_name)
	if (
		not block.is_ipv6
		or block.status != "Allocated"
		or block.virtual_machine
		or block.tenant_id not in (UNOWNED_TENANT_ID, 0)
	):
		frappe.throw(_("Select an allocated IPv6 block that no virtual machine holds."))

	validate_router_network(parse_ipv6_network(block.prefix))


def find_router(routes: list[dict[str, str]]) -> IPv6RouterServer | None:
	"""Return the router that carries the Internet range in these gateway routes."""
	gateway = next((route["gateway"] for route in routes if route["destination"] == ROUTED_DESTINATION), None)
	name = gateway and frappe.db.get_value(
		"IPv6 Router Server", {"wireguard_mesh_ipv6": gateway, "status": ["!=", "Archived"]}
	)
	return frappe.get_doc("IPv6 Router Server", name) if name else None


def get_routed_ipv6_address(virtual_machine: VirtualMachine) -> str:
	"""Return the public address that a router maps to the VM, or an empty string."""
	from atlas.vm.core.vm_service import VirtualMachineService

	router = find_router(VirtualMachineService(virtual_machine).get_gateway_routes())
	return router.get_public_address(virtual_machine) if router else ""


def detach_virtual_machine(virtual_machine: VirtualMachine) -> None:
	"""Remove the router route of a VM and keep its other gateway routes."""
	from atlas.vm.core.vm_service import VirtualMachineService

	service = VirtualMachineService(virtual_machine)
	routes = service.get_gateway_routes()
	if not find_router(routes):
		frappe.throw(_("This Virtual Machine has no routed IPv6 address."), exc=AtlasUserError)

	service.set_gateway_routes([route for route in routes if route["destination"] != ROUTED_DESTINATION])


@contextmanager
def ipv6_router_lifecycle_lock(name: str) -> Iterator[None]:
	"""Serialize lifecycle actions of one router."""
	try:
		with filelock(f"atlas:ipv6-router-server:{name}", timeout=0):
			yield
	except LockTimeoutError:
		frappe.throw(_("Another lifecycle action is in progress for IPv6 Router Server {0}.").format(name))


def _validate_system_manager() -> None:
	frappe.only_for("System Manager")
	user_type = frappe.get_cached_value("User", frappe.session.user, "user_type")
	if user_type != "System User":
		frappe.throw(_("Only System Users can manage IPv6 Router Servers."), frappe.PermissionError)


def enqueue_pending_ipv6_router_provisioning() -> None:
	"""Continue router setup after virtual machine reconciliation or an interrupted job."""
	for name in frappe.get_all(
		"IPv6 Router Server",
		filters={"status": ["in", ["Pending", "Provisioning"]], "virtual_machine": ["is", "set"]},
		pluck="name",
	):
		frappe.get_doc("IPv6 Router Server", name).enqueue_provisioning(enqueue_after_commit=False)
