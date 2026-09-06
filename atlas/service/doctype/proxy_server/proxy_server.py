# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

from typing import TYPE_CHECKING

import frappe
from frappe import _
from frappe.model.document import Document

if TYPE_CHECKING:
	from frappe.types import DF

CONTROL_API_PASSWORD_LENGTH = 48


class ProxyServer(Document):
	"""One regional HTTP proxy, and the virtual machine Atlas runs it on.

	The record owns its virtual machine. Provisioning creates it, installs the
	published package, and pushes the configuration file.
	"""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		control_api_password: DF.Password | None
		disk_mib: DF.Int
		failure_message: DF.SmallText | None
		installed_package_hash: DF.Data | None
		is_provisioning_completed: DF.Check
		memory_mib: DF.Int
		pushed_config_hash: DF.Data | None
		server_ip_address: DF.Link | None
		status: DF.Literal["Pending", "Provisioning", "Active", "Failed", "Archived"]
		tenant_id: DF.Int
		tls_expires_on: DF.Datetime | None
		vcpus: DF.Int
		virtual_machine: DF.Link | None
		virtual_machine_image: DF.Link
	# end: auto-generated types

	def before_insert(self) -> None:
		"""Create the credential the control API accepts. Atlas is its only holder."""
		self.control_api_password = frappe.generate_hash(length=CONTROL_API_PASSWORD_LENGTH)

	def validate(self) -> None:
		"""Reject a shape the proxy cannot run on."""
		if self.vcpus < 1 or self.memory_mib < 1 or self.disk_mib < 1:
			frappe.throw(_("A Proxy Server needs a positive CPU, memory, and disk size."))

	def after_insert(self) -> None:
		"""Build the proxy as soon as Frappe records it."""
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
	def archive(self) -> None:
		"""Terminate the virtual machine and release its address."""
		frappe.only_for("System Manager")
		if self.virtual_machine and frappe.db.exists("Virtual Machine", self.virtual_machine):
			frappe.get_doc("Virtual Machine", self.virtual_machine).terminate()

		self.db_set({"status": "Archived", "is_provisioning_completed": 0})
		frappe.msgprint(_("Proxy Server {0} is archived.").format(self.name))

	def validate_is_reachable(self) -> None:
		"""Reject an action that needs a running virtual machine."""
		if not self.virtual_machine:
			frappe.throw(_("Proxy Server {0} has no virtual machine yet.").format(self.name))

	def enqueue_provisioning(self) -> None:
		"""Queue one setup run. Every phase in it is safe to repeat."""
		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_provision",
			queue="long",
			timeout=3_600,
			job_id=f"atlas||proxy-server||provision||{self.name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)

	def _provision(self) -> None:
		from atlas.service.core.provisioning import ProxyServerProvisioner

		ProxyServerProvisioner(self).run()

	def _push_configuration(self) -> None:
		from atlas.service.core.provisioning import ProxyServerProvisioner

		provisioner = ProxyServerProvisioner(self)
		provisioner.push_configuration()
		provisioner.save_progress()

	def _install_package(self) -> None:
		from atlas.service.core.provisioning import ProxyServerProvisioner

		provisioner = ProxyServerProvisioner(self)
		provisioner.install_package()
		provisioner.save_progress()
