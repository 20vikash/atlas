from __future__ import annotations

import json
from typing import Any

import frappe
from frappe import _, request_cache
from frappe.model.document import Document
from frappe.utils import add_to_date, cint, now_datetime

from atlas.atlas.core.parsing import strict_bool
from atlas.vm.core.metal_models import MetalVirtualMachine
from atlas.vm.core.models import EGRESS_MODES
from atlas.vm.core.vm_service import VirtualMachineService

DRAFT_EXPIRY_MINUTES = 2
# Atlas WG Mesh reserves tenant 0 for the privileged tenant.
PRIVILEGED_TENANT_ID = 0


class VirtualMachine(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		disk_mib: DF.Int
		is_draft: DF.Check
		is_privileged: DF.Check
		is_terminating: DF.Check
		memory_mib: DF.Int
		metadata: DF.Code | None
		server: DF.Link
		tenant_id: DF.Int
		vcpus: DF.Int
		virtual_machine_image: DF.Link
	# end: auto-generated types

	@request_cache
	def get_metal_vm_info(self) -> MetalVirtualMachine | None:
		return VirtualMachineService(self).get_information()

	def before_insert(self) -> None:
		if not getattr(self.flags, "created_by_virtual_machine_api", False):
			frappe.throw(_("Create Virtual Machines from the Virtual Machine list."))

	def validate(self) -> None:
		"""A privileged VM must use tenant 0. Tenant 0 alone is not privileged.

		This runs on every save, because the flag is removable. Removing it drops
		the address from the next whitelist and ends cross-tenant traffic.
		"""
		if self.is_privileged and self.tenant_id != PRIVILEGED_TENANT_ID:
			frappe.throw(_("A privileged Virtual Machine must use tenant {0}.").format(PRIVILEGED_TENANT_ID))

	def on_trash(self) -> None:
		"""Delete only after Metal confirms that the VM is absent."""
		VirtualMachineService(self).validate_deletion()

	@property
	def current_state(self) -> str:
		if self.is_draft:
			return "unknown"

		if self.is_terminating:
			return "terminating"

		information = self.get_metal_vm_info()
		return information.observed.state if information else "unknown"

	@property
	def desired_state(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.desired.state if information else None

	@property
	def error(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.observed.error.message if information and information.observed.error else None

	@property
	def hostname(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.desired.guest.hostname if information else None

	@property
	def mac(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.observed.network.mac if information else None

	@property
	def egress(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.desired.network.egress if information else None

	@property
	def wireguard_mesh_ipv6(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.desired.network.wireguard_mesh_ipv6 if information else None

	@property
	def public_ipv4(self) -> str | None:
		information = self.get_metal_vm_info()
		return information.desired.network.public_ipv4 if information else None

	@property
	def disk_throughput_mibps(self) -> int:
		information = self.get_metal_vm_info()
		return information.desired.disk.throughput_mibps if information else 0

	@property
	def disk_iops(self) -> int:
		information = self.get_metal_vm_info()
		return information.desired.disk.iops if information else 0

	@property
	def private_network_throughput_mibps(self) -> int:
		information = self.get_metal_vm_info()
		return information.desired.network.private_network_throughput_mibps if information else 0

	@property
	def public_network_throughput_mibps(self) -> int:
		information = self.get_metal_vm_info()
		return information.desired.network.public_network_throughput_mibps if information else 0

	@property
	def ssh_keys(self) -> str:
		information = self.get_metal_vm_info()
		return "\n".join(information.desired.guest.ssh_keys) if information else ""

	@property
	def metadata(self) -> str:
		information = self.get_metal_vm_info()
		return json.dumps(information.desired.guest.metadata if information else {}, indent=2)

	@frappe.whitelist(methods=["POST"])
	def start(self) -> None:
		self.set_power_state("running")

	@frappe.whitelist(methods=["POST"])
	def stop(self) -> None:
		self.set_power_state("stopped")

	@frappe.whitelist(methods=["POST"])
	def pause(self) -> None:
		self.set_power_state("paused")

	@frappe.whitelist(methods=["POST"])
	def resume(self) -> None:
		self.set_power_state("running")

	@frappe.whitelist(methods=["POST"])
	def set_privileged(self, is_privileged: bool | int | str) -> None:
		frappe.only_for("System Manager")
		if self.is_draft or self.is_terminating:
			frappe.throw(_("Virtual Machine {0} is not ready for this change.").format(self.name))

		self.is_privileged = strict_bool(is_privileged, "is_privileged")
		self.save(ignore_permissions=True)

	@frappe.whitelist(methods=["POST"])
	def terminate(self) -> None:
		"""Ask Metal to remove this VM and release its IP address."""
		frappe.only_for("System Manager")
		VirtualMachineService(self).terminate()

	@frappe.whitelist(methods=["POST"])
	def create_machine_image(
		self,
		title: str,
		cache_image: bool = False,
		memory_snapshot: bool = False,
	) -> str:
		"""Queue a Machine image transfer from this VM."""
		frappe.only_for("System Manager")
		if self.is_draft:
			frappe.throw(_("Wait for Virtual Machine creation before creating an image."))
		title = title.strip()
		if not title:
			frappe.throw(_("Image title is required."))

		from atlas.vm.core.image_transfer import MachineImageTransferService

		return MachineImageTransferService().create_from_virtual_machine(
			self,
			title,
			cache_image=bool(cint(cache_image)),
			memory_snapshot=bool(cint(memory_snapshot)),
		)

	@frappe.whitelist(methods=["POST"])
	def replace_ssh_keys(self, ssh_keys: str | list[str]) -> dict[str, Any]:
		"""Replace all authorized SSH keys for this VM."""
		frappe.only_for("System Manager")
		values = frappe.parse_json(ssh_keys) if isinstance(ssh_keys, str) else ssh_keys
		if not isinstance(values, list) or any(not isinstance(value, str) for value in values):
			frappe.throw(_("SSH keys must be a list of strings."))

		return VirtualMachineService(self).replace_ssh_keys(values)

	@frappe.whitelist(methods=["POST"])
	def replace_metadata(self, metadata: dict[str, str]) -> dict[str, Any]:
		"""Replace all custom metadata for this VM with a plain string-to-string map."""
		frappe.only_for("System Manager")
		if not isinstance(metadata, dict) or any(
			not isinstance(key, str) or not isinstance(value, str) for key, value in metadata.items()
		):
			frappe.throw(_("Metadata must be a string-to-string map."))

		return VirtualMachineService(self).replace_metadata(metadata)

	@frappe.whitelist(methods=["POST"])
	def attach_ip_address(self, server_ip_address: str) -> dict[str, Any]:
		"""Attach one reserved public IPv4 address without a VM restart."""
		frappe.only_for("System Manager")
		self.validate_network_change()
		if frappe.db.exists("Server IP Address", {"virtual_machine": self.name}):
			frappe.throw(_("Detach the current public IPv4 address first."))

		return VirtualMachineService(self).attach_ip_address(server_ip_address)

	@frappe.whitelist(methods=["POST"])
	def detach_ip_address(self) -> dict[str, Any]:
		"""Remove the public IPv4 address without a VM restart."""
		frappe.only_for("System Manager")
		self.validate_network_change()
		if not frappe.db.exists("Server IP Address", {"virtual_machine": self.name}):
			frappe.throw(_("This Virtual Machine has no public IPv4 address."))

		return VirtualMachineService(self).detach_ip_address()

	@frappe.whitelist(methods=["POST"])
	def update_egress(self, egress: str) -> dict[str, Any]:
		"""Change internet reachability without a VM restart. Mesh reachability does not change."""
		frappe.only_for("System Manager")
		if egress not in EGRESS_MODES:
			frappe.throw(_("Egress must be uplink, mesh, or none."))
		if egress != "uplink" and frappe.db.exists("Server IP Address", {"virtual_machine": self.name}):
			frappe.throw(_("Detach the public IPv4 address before you remove the internet path."))

		return self.update_network(egress=egress)

	@frappe.whitelist(methods=["POST"])
	def update_network_throughput(
		self, private_network_throughput_mibps: int, public_network_throughput_mibps: int
	) -> dict[str, Any]:
		"""Change the throughput limits in MiB/s without a VM restart. A value of 0 removes the limit."""
		frappe.only_for("System Manager")
		return self.update_network(
			private_network_throughput_mibps=self.parse_throughput(private_network_throughput_mibps),
			public_network_throughput_mibps=self.parse_throughput(public_network_throughput_mibps),
		)

	@frappe.whitelist(methods=["POST"])
	def update_disk_limits(self, disk_throughput_mibps: int, disk_iops: int) -> dict[str, Any]:
		"""Change the disk limits in MiB/s and IOPS without a VM restart. 0 removes a limit."""
		frappe.only_for("System Manager")
		if self.is_draft:
			frappe.throw(_("Wait for Virtual Machine creation before a disk change."))

		return VirtualMachineService(self).update_disk_limits(
			self.parse_throughput(disk_throughput_mibps),
			self.parse_throughput(disk_iops),
		)

	def parse_throughput(self, value: object) -> int:
		"""Return one throughput limit in MiB/s. A malformed value is an error, not 0."""
		try:
			throughput = int(str(value).strip())
		except TypeError, ValueError:
			frappe.throw(_("Network throughput must be a whole number of MiB/s."))
			raise AssertionError from None
		if throughput < 0:
			frappe.throw(_("Network throughput must not be negative."))
		return throughput

	def update_network(self, **changes: Any) -> dict[str, Any]:
		"""Send the complete desired network settings to Metal.

		Metal replaces every mutable setting, so unchanged values come from the
		live Metal state instead of a local default.
		"""
		self.validate_network_change()
		return VirtualMachineService(self).update_network(changes)

	def validate_network_change(self) -> None:
		"""Reject a network change while the request is not ready."""
		if self.is_draft:
			frappe.throw(_("Wait for Virtual Machine creation before a network change."))
		if self.is_terminating:
			frappe.throw(_("Virtual Machine {0} is terminating.").format(self.name))

	@frappe.whitelist(methods=["POST"])
	def resize_disk(self, disk_mib: int) -> None:
		"""Ask Metal to increase this VM disk size."""
		frappe.only_for("System Manager")
		VirtualMachineService(self).resize_disk(int(disk_mib))

	@frappe.whitelist(methods=["POST"])
	def resize_compute(self, vcpus: int, memory_mib: int) -> None:
		"""Ask Metal to change this VM CPU and memory. The VM must be stopped."""
		frappe.only_for("System Manager")
		VirtualMachineService(self).set_compute(int(vcpus), int(memory_mib))

	def set_power_state(self, state: str) -> None:
		"""Ask Metal to store one desired power state."""
		frappe.only_for("System Manager")
		VirtualMachineService(self).set_power_state(state)

	@frappe.whitelist(methods=["POST"])
	def reboot(self) -> None:
		"""Request an in-place VM restart."""
		frappe.only_for("System Manager")
		VirtualMachineService(self).request_restart()

	@frappe.whitelist(methods=["POST"])
	def get_console_token(self, mode: str = "tty") -> dict[str, str]:
		"""Issue a one-time token to open this VM console in tty or ssh mode."""
		frappe.only_for("System Manager")
		if mode not in {"tty", "ssh"}:
			frappe.throw(_("Console mode must be tty or ssh."))
		from atlas.vm.core.console_token import issue_console_token

		connection = VirtualMachineService(self).get_console_connection(mode)
		return {"token": issue_console_token(connection)}


@frappe.whitelist(methods=["POST"])
def create(request: str | dict[str, Any]) -> dict[str, str | bool]:
	"""Create one Atlas VM request and send its intent to Metal."""
	frappe.only_for("System Manager")
	return VirtualMachineService.create(request)


def reconcile_stale_drafts() -> None:
	"""Resolve old drafts without deletion when the result is uncertain."""
	cutoff = add_to_date(now_datetime(), minutes=-DRAFT_EXPIRY_MINUTES)
	names = frappe.get_all(
		"Virtual Machine",
		filters={"is_draft": 1, "creation": ["<", cutoff]},
		pluck="name",
	)
	for name in names:
		reconcile_stale_draft(name)


def reconcile_stale_draft(name: str) -> None:
	"""Finalize a present VM or delete a confirmed absent draft."""
	VirtualMachineService.reconcile_stale_draft(name)


def reconcile_terminating_virtual_machines() -> None:
	"""Delete terminated VMs that Metal reports as absent."""
	names = frappe.get_all("Virtual Machine", filters={"is_terminating": 1}, pluck="name")
	for name in names:
		reconcile_terminating_virtual_machine(name)


def reconcile_terminating_virtual_machine(name: str) -> None:
	"""Delete a terminated VM once Metal confirms it is absent."""
	VirtualMachineService.reconcile_terminating(name)
