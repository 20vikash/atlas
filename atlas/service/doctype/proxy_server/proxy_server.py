# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

from typing import TYPE_CHECKING, Any

import frappe
from frappe import _
from frappe.model.document import Document

if TYPE_CHECKING:
	from frappe.types import DF

CONTROL_API_PASSWORD_LENGTH = 48


class ProxyServer(Document):
	"""One regional HTTP proxy and its virtual machine."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		control_api_password: DF.Password | None
		failure_message: DF.SmallText | None
		installed_package_hash: DF.Data | None
		is_provisioning_completed: DF.Check
		pushed_config_hash: DF.Data | None
		status: DF.Literal["Pending", "Provisioning", "Active", "Failed", "Archived"]
		tls_expires_on: DF.Datetime | None
		virtual_machine: DF.Link | None
	# end: auto-generated types

	@property
	def public_ipv4(self) -> str | None:
		"""Return the public IPv4 address of the proxy virtual machine."""
		if not self.virtual_machine:
			return None

		return frappe.get_doc("Virtual Machine", self.virtual_machine).public_ipv4

	def before_insert(self) -> None:
		"""Reject records created outside the Proxy Server API."""
		if not getattr(self.flags, "created_by_proxy_server_api", False):
			frappe.throw(_("Create Proxy Servers from the Proxy Server list."))

		self.control_api_password = frappe.generate_hash(length=CONTROL_API_PASSWORD_LENGTH)

	def get_domain(self) -> str:
		"""Return the name of this proxy below the Atlas wildcard domain."""
		wildcard_domain = frappe.get_cached_value("Atlas Settings", "Atlas Settings", "wildcard_domain")
		return f"{self.name}.{wildcard_domain}"

	def after_insert(self) -> None:
		"""Queue proxy setup after Frappe records the proxy."""
		if not getattr(self.flags, "skip_initial_provisioning", False):
			self.enqueue_provisioning()

	def on_trash(self) -> None:
		"""Refuse deletion while the virtual machine exists."""
		if self.virtual_machine and frappe.db.exists("Virtual Machine", self.virtual_machine):
			frappe.throw(_("Archive Proxy Server {0} before you delete it.").format(self.name))

	@frappe.whitelist(methods=["POST"])
	def provision(self) -> None:
		"""Run the setup sequence again after a failure."""
		frappe.only_for("System Manager")
		if self.status == "Archived":
			frappe.throw(_("Proxy Server {0} is archived.").format(self.name))

		self.enqueue_provisioning()
		frappe.msgprint(_("Proxy Server setup has been queued. Please check after some time."))

	@frappe.whitelist(methods=["POST"])
	def push_configuration(self) -> None:
		"""Send the current credentials and wildcard certificate to the proxy."""
		frappe.only_for("System Manager")
		self.validate_is_reachable()

		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_push_configuration",
			queue="long",
			timeout=600,
			job_id=f"atlas||proxy-server||configure||{self.name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)
		frappe.msgprint(_("The configuration push has been queued. Please check after some time."))

	@frappe.whitelist(methods=["POST"])
	def install_package(self) -> None:
		"""Install the published HTTP proxy package again."""
		frappe.only_for("System Manager")
		self.validate_is_reachable()

		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_install_package",
			queue="long",
			timeout=1_800,
			job_id=f"atlas||proxy-server||install||{self.name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)
		frappe.msgprint(_("The package install has been queued. Please check after some time."))

	@frappe.whitelist(methods=["POST"])
	def update_dns_record(self) -> None:
		"""Point the proxy name at its current address."""
		frappe.only_for("System Manager")
		if self.status == "Archived":
			frappe.throw(_("Proxy Server {0} is archived.").format(self.name))

		from atlas.service.core.proxy.provisioning import ProxyServerProvisioner

		provisioner = ProxyServerProvisioner(self)
		provisioner.update_dns_record()
		if provisioner.proxy_server.status == "Archived":
			frappe.throw(_("Proxy Server {0} is archived.").format(self.name))

		frappe.msgprint(
			_("{0} now points at {1}.").format(
				provisioner.proxy_server.get_domain(), provisioner.proxy_server.public_ipv4
			)
		)

	@frappe.whitelist(methods=["POST"])
	def archive(self) -> None:
		"""Terminate the virtual machine, release its address, and remove its name."""
		frappe.only_for("System Manager")
		proxy_server = frappe.get_doc(self.doctype, self.name, for_update=True)
		if proxy_server.status == "Archived":
			return

		proxy_server.remove_dns_record()
		if proxy_server.virtual_machine and frappe.db.exists("Virtual Machine", proxy_server.virtual_machine):
			frappe.get_doc("Virtual Machine", proxy_server.virtual_machine).terminate()

		proxy_server.db_set({"status": "Archived", "is_provisioning_completed": 0})
		frappe.msgprint(_("Proxy Server {0} is archived.").format(proxy_server.name))

	def remove_dns_record(self) -> None:
		"""Remove the proxy DNS record before its address is released."""
		frappe.get_single("Atlas Settings").dns_provider_controller.remove_a_record(self.get_domain())

	def validate_is_reachable(self) -> None:
		"""Reject an action that needs a running virtual machine."""
		if not self.virtual_machine:
			frappe.throw(_("Proxy Server {0} has no virtual machine yet.").format(self.name))

	def enqueue_provisioning(self) -> None:
		"""Queue proxy setup."""
		enqueue_proxy_provisioning(self.name)

	def _provision(self) -> None:
		from atlas.service.core.proxy.provisioning import ProxyServerProvisioner

		ProxyServerProvisioner(self).run()

	def _push_configuration(self) -> None:
		from atlas.service.core.proxy.provisioning import ProxyServerProvisioner

		ProxyServerProvisioner(self).push_configuration()

	def _install_package(self) -> None:
		from atlas.service.core.proxy.provisioning import ProxyServerProvisioner

		ProxyServerProvisioner(self).install_package()


@frappe.whitelist(methods=["POST"])
def create(request: str | dict[str, Any]) -> dict[str, str | bool]:
	"""Create a Proxy Server and its virtual machine."""
	frappe.only_for("System Manager")
	values = frappe.parse_json(request) if isinstance(request, str) else request
	if not isinstance(values, dict):
		frappe.throw(_("Proxy Server creation data must be an object."))

	proxy_server = frappe.new_doc("Proxy Server")
	proxy_server.flags.created_by_proxy_server_api = True
	proxy_server.flags.skip_initial_provisioning = True
	proxy_server.insert(ignore_permissions=True)

	from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import reserve
	from atlas.vm.core.vm_service import VirtualMachineCreateError, VirtualMachineService

	virtual_machine_request = {
		"virtual_machine_image": values.get("virtual_machine_image"),
		"vcpus": values.get("vcpus"),
		"memory_mib": values.get("memory_mib"),
		"disk_mib": values.get("disk_mib"),
		"tenant_id": 0,
		"hostname": proxy_server.name,
		"ssh_keys": frappe.get_single("Atlas Settings").public_ssh_key,
		"egress": "uplink",
		"server_ip_address": reserve(),
	}
	try:
		result = VirtualMachineService.create(virtual_machine_request)
		proxy_server.virtual_machine = result["name"]
		is_draft = bool(result["is_draft"])
	except VirtualMachineCreateError as error:
		proxy_server.virtual_machine = error.virtual_machine_name
		proxy_server.status = "Failed"
		proxy_server.failure_message = f"virtual-machine: {error}"
		is_draft = True

	proxy_server.save(ignore_permissions=True)
	proxy_server.enqueue_provisioning()
	return {"name": proxy_server.name, "is_draft": is_draft}


def enqueue_pending_proxies_provisioning() -> None:
	"""Queue setup for every pending Proxy Server."""
	for name in frappe.get_all("Proxy Server", filters={"status": "Pending"}, pluck="name"):
		enqueue_proxy_provisioning(name, enqueue_after_commit=False)


def enqueue_proxy_provisioning(proxy_server_name: str, enqueue_after_commit: bool = True) -> None:
	"""Queue setup for one Proxy Server."""
	frappe.enqueue_doc(
		"Proxy Server",
		proxy_server_name,
		"_provision",
		queue="long",
		timeout=3_600,
		job_id=f"atlas||proxy-server||provision||{proxy_server_name}",
		deduplicate=True,
		enqueue_after_commit=enqueue_after_commit,
	)
