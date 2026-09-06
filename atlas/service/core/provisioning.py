from __future__ import annotations

import logging
from collections.abc import Callable
from typing import TYPE_CHECKING

import frappe
from frappe import _

from atlas.atlas.core.artifacts import get_download_url
from atlas.atlas.core.ssh import SSHRunner, wait_for_server
from atlas.atlas.doctype.ssh_task.ssh_task import SSHTask
from atlas.service.core.configuration import ProxyConfiguration
from atlas.service.core.http_proxy_package import SETTINGS_FILE_FIELD, SETTINGS_HASH_FIELD

if TYPE_CHECKING:
	from atlas.service.doctype.proxy_server.proxy_server import ProxyServer

INSTALL_TIMEOUT_SECONDS = 1_800
CONFIGURE_TIMEOUT_SECONDS = 300
SSH_TIMEOUT_SECONDS = 600
SSH_POLL_INTERVAL_SECONDS = 5


class ProxyServerProvisioner:
	"""Own the safe proxy setup sequence and its durable progress.

	Every phase is safe to repeat. A failed run resumes at the phase that failed
	instead of building a second virtual machine.
	"""

	progress_fields = (
		"status",
		"virtual_machine",
		"server_ip_address",
		"installed_package_hash",
		"pushed_config_hash",
		"tls_expires_on",
		"is_provisioning_completed",
		"failure_message",
	)

	def __init__(self, proxy_server: "ProxyServer") -> None:
		self.proxy_server = proxy_server
		self.logger = logging.getLogger("atlas.service.provisioning")

	def run(self) -> None:
		"""Run each proxy setup step in order."""
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
			self.logger.exception(
				"Proxy provisioning failed",
				extra={"resource": self.proxy_server.name, "operation": "provision", "phase": phase},
			)
			frappe.log_error(title=f"Proxy provisioning failed for {self.proxy_server.name} during {phase}")
			raise

	@property
	def steps(self) -> tuple[tuple[str, Callable[[], None]], ...]:
		"""Return the proxy setup steps in execution order.

		The package installs `proxy-control`. The configuration step then applies
		the wildcard certificate and credentials.
		"""
		return (
			("virtual-machine", self.create_virtual_machine),
			("secure-shell", self.wait_for_ssh),
			("package", self.install_package),
			("configuration", self.push_configuration),
		)

	def run_step(self, phase: str, operation: Callable[[], None]) -> None:
		"""Run one setup step and save its resulting Proxy Server fields."""
		self.logger.info(
			"Proxy provisioning step started",
			extra={"resource": self.proxy_server.name, "operation": "provision", "phase": phase},
		)
		operation()
		self.save_progress()

	def create_virtual_machine(self) -> None:
		"""Create the virtual machine that runs the proxy, and give Atlas access to it.

		The Atlas public key goes in at create time, so the controller can reach
		the guest as soon as it boots. A proxy serves public traffic, so it needs
		a public IPv4 address and uplink egress anyway.
		"""
		if self.proxy_server.virtual_machine:
			if not frappe.db.exists("Virtual Machine", self.proxy_server.virtual_machine):
				self.proxy_server.virtual_machine = None
				self.save_progress()
			else:
				virtual_machine = frappe.get_doc("Virtual Machine", self.proxy_server.virtual_machine)
				if virtual_machine.is_draft:
					frappe.throw(
						_(
							"Wait for Virtual Machine {0} creation reconciliation before you provision again."
						).format(virtual_machine.name)
					)
				return

		from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import reserve
		from atlas.vm.core.vm_service import VirtualMachineCreateError, VirtualMachineService

		settings = frappe.get_single("Atlas Settings")
		if not self.proxy_server.server_ip_address:
			self.proxy_server.server_ip_address = reserve()
			self.save_progress()

		try:
			result = VirtualMachineService.create(
				{
					"virtual_machine_image": self.proxy_server.virtual_machine_image,
					"vcpus": self.proxy_server.vcpus,
					"memory_mib": self.proxy_server.memory_mib,
					"disk_mib": self.proxy_server.disk_mib,
					"tenant_id": self.proxy_server.tenant_id,
					"hostname": self.proxy_server.name,
					"ssh_keys": settings.public_ssh_key,
					"egress": "uplink",
					"server_ip_address": self.proxy_server.server_ip_address,
				}
			)
		except VirtualMachineCreateError as error:
			self.proxy_server.virtual_machine = error.virtual_machine_name
			self.save_progress()
			raise
		self.proxy_server.virtual_machine = result["name"]
		self.save_progress()

		# Metal never confirmed the create, so the VM reconciler settles it first.
		if result["is_draft"]:
			frappe.throw(
				_("Metal did not confirm Virtual Machine {0}. Provision again after it settles.").format(
					result["name"]
				)
			)

	def wait_for_ssh(self) -> None:
		"""Wait until the guest answers as root on its public address."""
		wait_for_server(
			host=self.ssh_host,
			users=("root",),
			timeout_seconds=SSH_TIMEOUT_SECONDS,
			poll_interval_seconds=SSH_POLL_INTERVAL_SECONDS,
		)

	def push_configuration(self) -> None:
		"""Write the configuration file and apply it.

		This does not use an `SSH Task`, because the payload carries the wildcard
		private key and the control credential, and a task stores its script as
		plain text. Only the outcome is recorded.
		"""
		configuration = ProxyConfiguration(self.proxy_server)
		if self.proxy_server.pushed_config_hash == configuration.digest:
			return

		result = SSHRunner(self.ssh_host).run_command(
			configuration.get_push_command(), timeout_seconds=CONFIGURE_TIMEOUT_SECONDS
		)
		if not result.is_success:
			frappe.throw(_("The proxy rejected its configuration: {0}").format(result.output.strip()))

		self.proxy_server.pushed_config_hash = configuration.digest
		self.proxy_server.tls_expires_on = frappe.get_single("Atlas Settings").wildcard_tls_expires_on

	def install_package(self) -> None:
		"""Download the published package and run the proxy setup script."""
		settings = frappe.get_single("Atlas Settings")
		package_file = settings.get(SETTINGS_FILE_FIELD)
		package_hash = settings.get(SETTINGS_HASH_FIELD)
		if not package_file or not package_hash:
			frappe.throw(_("Atlas Settings holds no HTTP proxy package. Run build-http-proxy-package."))

		if self.proxy_server.installed_package_hash == package_hash:
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

	@property
	def ssh_host(self) -> str:
		"""Return the address the controller connects to."""
		virtual_machine = frappe.get_doc("Virtual Machine", self.proxy_server.virtual_machine)
		return virtual_machine.ssh_host

	def save_progress(self) -> None:
		"""Store the current fields and commit, so a failed run resumes from disk."""
		self.proxy_server.db_set({field: self.proxy_server.get(field) for field in self.progress_fields})
		frappe.db.commit()  # nosemgrep
