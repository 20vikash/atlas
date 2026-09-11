# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

from contextlib import ExitStack, contextmanager
from typing import TYPE_CHECKING, Any

import frappe
from frappe import _
from frappe.model.document import Document
from frappe.utils.file_lock import LockTimeoutError
from frappe.utils.synchronization import filelock

if TYPE_CHECKING:
	from collections.abc import Iterator

LIFECYCLE_LOCK_NAME = "atlas:cargo-server:lifecycle"
PROVISIONABLE_STATUSES = ("Not Provisioned", "Archived")


class CargoServer(Document):
	"""Own the regional Cargo service and its virtual machine."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		completion: DF.Percent
		failure_message: DF.SmallText | None
		installation_task: DF.Link | None
		status: DF.Literal["Not Provisioned", "Pending", "Provisioning", "Active", "Failed", "Archived"]
		virtual_machine: DF.Link | None
	# end: auto-generated types

	@property
	def domain(self) -> str:
		"""Return the public Cargo domain."""
		wildcard_domain = frappe.get_cached_value("Atlas Settings", "Atlas Settings", "wildcard_domain")
		return f"cargo.{wildcard_domain}"

	@frappe.whitelist(methods=["POST"])
	def provision(self, request: str | dict[str, Any]) -> dict[str, str | bool]:
		"""Create and queue setup for the regional Cargo virtual machine."""
		_validate_system_manager()
		values = frappe.parse_json(request) if isinstance(request, str) else request
		if not isinstance(values, dict):
			frappe.throw(_("Cargo Server provisioning data must be an object."))

		server_ip_address = values.get("server_ip_address")
		if not isinstance(server_ip_address, str) or not server_ip_address.strip():
			frappe.throw(_("Select an allocated public IPv4 address."))

		with cargo_lifecycle_lock():
			cargo_server: CargoServer = frappe.get_single("Cargo Server")
			cargo_server._validate_provisioning_state()
			cargo_server._validate_active_proxy()
			cargo_server._validate_system_image(values.get("virtual_machine_image"))
			cargo_server.status = "Pending"
			cargo_server.completion = 0
			cargo_server.failure_message = None
			cargo_server.installation_task = None
			cargo_server.save(ignore_permissions=True)

			is_draft = cargo_server._create_virtual_machine(values)
			cargo_server.save(ignore_permissions=True)
			if cargo_server.status != "Failed":
				cargo_server.enqueue_provisioning()

		if cargo_server.status == "Failed":
			frappe.msgprint(_("Cargo virtual machine creation failed. Archive it before you try again."))
		else:
			frappe.msgprint(_("Cargo Server setup has been queued. Please check after some time."))
		return {"name": cargo_server.name, "is_draft": is_draft}

	@frappe.whitelist(methods=["POST"])
	def archive(self) -> None:
		"""Remove the proxy routes and terminate the Cargo virtual machine."""
		_validate_system_manager()
		with cargo_lifecycle_lock():
			cargo_server = frappe.get_single("Cargo Server")
			if not cargo_server.virtual_machine:
				if cargo_server.status == "Archived":
					return
				frappe.throw(_("Cargo Server has no virtual machine to archive."))

			try:
				from atlas.service.core.cargo.provisioning import CargoServerProvisioner

				CargoServerProvisioner(cargo_server).remove_proxy_routes()
				if frappe.db.exists("Virtual Machine", cargo_server.virtual_machine):
					frappe.get_doc("Virtual Machine", cargo_server.virtual_machine).terminate()
			except Exception as error:
				cargo_server.status = "Failed"
				cargo_server.failure_message = failure_message("archive", error)
				cargo_server.save(ignore_permissions=True)
				frappe.db.commit()  # nosemgrep
				raise

			cargo_server.status = "Archived"
			cargo_server.completion = 0
			cargo_server.failure_message = None
			cargo_server.virtual_machine = None
			cargo_server.installation_task = None
			cargo_server.save(ignore_permissions=True)

		frappe.msgprint(_("Cargo Server is archived."))

	def enqueue_provisioning(self, enqueue_after_commit: bool = True) -> None:
		"""Queue Cargo installation for the attached virtual machine."""
		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_provision",
			queue="long",
			timeout=3_600,
			job_id="atlas||cargo-server||provision",
			deduplicate=True,
			enqueue_after_commit=enqueue_after_commit,
		)

	def _provision(self) -> None:
		from atlas.service.core.cargo.provisioning import CargoServerProvisioner

		CargoServerProvisioner(self).run()

	def _validate_provisioning_state(self) -> None:
		if self.virtual_machine:
			frappe.throw(_("Archive the current Cargo Server before you provision another one."))
		if self.status not in PROVISIONABLE_STATUSES:
			frappe.throw(_("Cargo Server cannot be provisioned while its status is {0}.").format(self.status))

	def _validate_active_proxy(self) -> None:
		if not frappe.db.exists("Proxy Server", {"status": "Active"}):
			frappe.throw(_("Provision an Active Proxy Server before you provision Cargo Server."))

	def _validate_system_image(self, image_name: object) -> None:
		if not isinstance(image_name, str) or not image_name:
			frappe.throw(_("Select a System Virtual Machine Image."))
		if frappe.db.get_value("Virtual Machine Image", image_name, "image_type") != "system":
			frappe.throw(_("Select a System Virtual Machine Image."))

	def _create_virtual_machine(self, values: dict[str, Any]) -> bool:
		from atlas.vm.core.vm_service import VirtualMachineCreateError, VirtualMachineService

		settings = frappe.get_single("Atlas Settings")
		request = {
			"virtual_machine_image": values.get("virtual_machine_image"),
			"vcpus": values.get("vcpus"),
			"memory_mib": values.get("memory_mib"),
			"disk_mib": values.get("disk_mib"),
			"tenant_id": 0,
			"is_privileged": True,
			"hostname": "cargo",
			"ssh_keys": settings.public_ssh_key,
			"egress": "uplink",
			"server_ip_address": values["server_ip_address"],
		}
		try:
			result = VirtualMachineService.create(request)
			self.virtual_machine = result["name"]
			return bool(result["is_draft"])
		except VirtualMachineCreateError as error:
			self.virtual_machine = error.virtual_machine_name
			self.status = "Failed"
			self.failure_message = failure_message("virtual-machine", error)
			return True


@contextmanager
def cargo_lifecycle_lock() -> Iterator[None]:
	"""Hold the site file lock that serializes Cargo lifecycle changes."""
	with ExitStack() as stack:
		try:
			stack.enter_context(filelock(LIFECYCLE_LOCK_NAME, timeout=0))
		except LockTimeoutError:
			frappe.throw(_("Another Cargo Server lifecycle action is in progress."))

		yield


def failure_message(phase: str, error: Exception) -> str:
	"""Return a bounded one-line failure without installation credentials."""
	message = " ".join(str(error).split()) or error.__class__.__name__
	return f"{phase}: {message}"[:500]


def _validate_system_manager() -> None:
	frappe.only_for("System Manager")
	user_type = frappe.get_cached_value("User", frappe.session.user, "user_type")
	if user_type != "System User":
		frappe.throw(_("Only System Users can manage Cargo Server."), frappe.PermissionError)


def enqueue_pending_cargo_provisioning() -> None:
	"""Continue a pending Cargo setup after virtual machine reconciliation."""
	cargo_server = frappe.get_single("Cargo Server")
	if cargo_server.status == "Pending" and cargo_server.virtual_machine:
		cargo_server.enqueue_provisioning(enqueue_after_commit=False)
