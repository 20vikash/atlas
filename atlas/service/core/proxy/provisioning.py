from __future__ import annotations

import logging
from collections.abc import Callable
from typing import TYPE_CHECKING

import frappe
from frappe import _

from atlas.atlas.core.artifacts import get_download_url
from atlas.atlas.core.ssh import SSHRunner, wait_for_server
from atlas.atlas.doctype.ssh_task.ssh_task import SSHTask
from atlas.service.core.proxy.configuration import ProxyConfiguration

if TYPE_CHECKING:
	from atlas.service.doctype.proxy_server.proxy_server import ProxyServer

INSTALL_TIMEOUT_SECONDS = 1_800
CONFIGURE_TIMEOUT_SECONDS = 300
SSH_TIMEOUT_SECONDS = 600
SSH_POLL_INTERVAL_SECONDS = 5


class ProxyServerProvisioner:
	"""Run proxy setup and store its progress."""

	def __init__(self, proxy_server: "ProxyServer") -> None:
		self.proxy_server = proxy_server
		self.logger = logging.getLogger("atlas.service.provisioning")

	def run(self) -> None:
		"""Run each proxy setup step in order."""
		if not self.is_virtual_machine_ready:
			return

		phase = "start"
		self.proxy_server.status = "Provisioning"
		self.proxy_server.failure_message = None
		self.save_progress()
		try:
			for phase, operation in self.steps:
				self.run_step(phase, operation)

			self.proxy_server.status = "Active"
			self.proxy_server.is_provisioning_completed = 1
			self.save_progress()
		except Exception as error:
			self.proxy_server.status = "Failed"
			self.proxy_server.failure_message = f"{phase}: {error}"
			self.save_progress()
			frappe.db.commit()  # nosemgrep
			self.logger.exception(
				"Proxy provisioning failed",
				extra={"resource": self.proxy_server.name, "operation": "provision", "phase": phase},
			)
			frappe.log_error(title=f"Proxy provisioning failed for {self.proxy_server.name} during {phase}")
			raise

	@property
	def steps(self) -> tuple[tuple[str, Callable[[bool], None]], ...]:
		"""Return setup steps in execution order."""
		return (
			("virtual-machine", self.validate_virtual_machine),
			("dns", self.update_dns_record),
			("secure-shell", self.wait_for_ssh),
			("package", self.install_package),
			("configuration", self.push_configuration),
		)

	@property
	def is_virtual_machine_ready(self) -> bool:
		"""Report whether the proxy virtual machine left the draft state."""
		if not self.proxy_server.virtual_machine:
			return False

		virtual_machine = frappe.get_doc("Virtual Machine", self.proxy_server.virtual_machine)
		return not virtual_machine.is_draft

	def run_step(self, phase: str, operation: Callable[[bool], None]) -> None:
		"""Run one setup step and save its resulting Proxy Server fields."""
		self.logger.info(
			"Proxy provisioning step started",
			extra={"resource": self.proxy_server.name, "operation": "provision", "phase": phase},
		)
		operation(save=False)
		self.save_progress()

	def validate_virtual_machine(self, save: bool = True) -> None:
		"""Confirm that the proxy virtual machine is ready."""
		if not self.proxy_server.virtual_machine:
			frappe.throw(_("Proxy Server {0} has no virtual machine.").format(self.proxy_server.name))

		virtual_machine = frappe.get_doc("Virtual Machine", self.proxy_server.virtual_machine)
		if virtual_machine.is_draft:
			frappe.throw(
				_("Wait for Virtual Machine {0} creation reconciliation before you provision again.").format(
					virtual_machine.name
				)
			)

		self.save_progress(save)

	def update_dns_record(self, save: bool = True) -> None:
		"""Point the proxy domain at its public IPv4 address."""
		self.proxy_server = frappe.get_doc("Proxy Server", self.proxy_server.name, for_update=True)
		if self.proxy_server.status == "Archived":
			return

		address = self.proxy_server.public_ipv4
		if not address:
			frappe.throw(_("Proxy Server {0} has no public IPv4 address.").format(self.proxy_server.name))

		settings = frappe.get_single("Atlas Settings")
		settings.dns_provider_controller.upsert_a_record(self.proxy_server.get_domain(), address)
		self.save_progress(save)

	def wait_for_ssh(self, save: bool = True) -> None:
		"""Wait until the guest answers as root on its public address."""
		wait_for_server(
			host=self.ssh_host,
			users=("root",),
			timeout_seconds=SSH_TIMEOUT_SECONDS,
			poll_interval_seconds=SSH_POLL_INTERVAL_SECONDS,
		)
		self.save_progress(save)

	def push_configuration(self, save: bool = True) -> None:
		"""Write and apply the proxy configuration."""
		configuration = ProxyConfiguration(self.proxy_server)
		if self.proxy_server.pushed_config_hash == configuration.digest:
			self.save_progress(save)
			return

		write = SSHRunner(self.ssh_host).run_command(
			configuration.get_write_command(), timeout_seconds=CONFIGURE_TIMEOUT_SECONDS
		)
		if not write.is_success:
			frappe.throw(
				_("The proxy did not accept its configuration file: {0}").format(write.output.strip())
			)

		task = SSHTask.create_for_command(
			target_type="Virtual Machine",
			target=self.proxy_server.virtual_machine,
			command=configuration.get_apply_command(),
			timeout_seconds=CONFIGURE_TIMEOUT_SECONDS,
			run_in_background=False,
		)
		result = task.result
		if result is None or not result.is_success:
			frappe.throw(_("The proxy rejected its configuration. See SSH Task {0}.").format(task.name))

		self.proxy_server.pushed_config_hash = configuration.digest
		self.proxy_server.tls_expires_on = frappe.get_single("Atlas Settings").wildcard_tls_expires_on
		self.save_progress(save)

	def install_package(self, save: bool = True) -> None:
		"""Download the published package and run the proxy setup script."""
		settings = frappe.get_single("Atlas Settings")
		package_file = settings.get("http_proxy_package_file")
		package_hash = settings.get("http_proxy_package_hash")
		if not package_file or not package_hash:
			frappe.throw(_("Atlas Settings holds no HTTP proxy package. Run build-http-proxy-package."))

		if self.proxy_server.installed_package_hash == package_hash:
			self.save_progress(save)
			return

		task = SSHTask.create_for_script_file(
			target_type="Virtual Machine",
			target=self.proxy_server.virtual_machine,
			script_path="install-http-proxy.sh",
			environment={
				"HTTP_PROXY_DOWNLOAD_URL": get_download_url(package_file),
				"HTTP_PROXY_PACKAGE_SHA256": package_hash,
			},
			timeout_seconds=INSTALL_TIMEOUT_SECONDS,
			run_in_background=False,
		)
		result = task.result
		if result is None or not result.is_success:
			frappe.throw(_("The HTTP proxy install failed. See SSH Task {0}.").format(task.name))

		self.proxy_server.installed_package_hash = package_hash
		self.save_progress(save)

	@property
	def ssh_host(self) -> str:
		"""Return the address the controller connects to."""
		virtual_machine = frappe.get_doc("Virtual Machine", self.proxy_server.virtual_machine)
		return virtual_machine.ssh_host

	def save_progress(self, save: bool = True) -> None:
		"""Store the current proxy state."""
		if save:
			self.proxy_server.save(ignore_permissions=True)
