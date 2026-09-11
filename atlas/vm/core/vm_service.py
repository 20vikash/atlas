from __future__ import annotations

from collections.abc import Callable
from typing import TYPE_CHECKING, Any, Never, TypedDict, cast

import frappe
from frappe import _

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.atlas.core.mesh_address import get_virtual_machine_mesh_address
from atlas.vm.core.metal_client import MetalClient, MetalClientError
from atlas.vm.core.metal_models import MetalVirtualMachine
from atlas.vm.core.models import EGRESS_MODES, VirtualMachineCreateRequest
from atlas.vm.core.placement import PlacementService

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer
	from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import (
		MetalServerIPAddress,
	)
	from atlas.vm.doctype.virtual_machine.virtual_machine import VirtualMachine
	from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage


class MetalOperationError(frappe.ValidationError):
	"""Report that the assigned host could not complete the request."""

	http_status_code = 502


class VirtualMachineCreateError(MetalOperationError):
	"""Report a failed create and the draft that records it."""

	def __init__(self, virtual_machine_name: str, error: MetalClientError) -> None:
		super().__init__(_("Metal request failed: {0}").format(error))
		self.virtual_machine_name = virtual_machine_name


class VirtualMachineCreateResult(TypedDict):
	"""The stored result of one virtual machine create request."""

	name: str
	is_draft: bool


class VirtualMachineService:
	"""Own Atlas orchestration for one virtual machine."""

	def __init__(self, virtual_machine: VirtualMachine) -> None:
		self.virtual_machine = virtual_machine

	@classmethod
	def create(cls, value: str | dict[str, Any] | VirtualMachineCreateRequest) -> VirtualMachineCreateResult:
		"""Create and commit an Atlas request before the Metal request."""
		if isinstance(value, VirtualMachineCreateRequest):
			request = value
		else:
			try:
				request = VirtualMachineCreateRequest.from_value(value)
			except ValueError as error:
				frappe.throw(_(str(error)))
				raise AssertionError from error

		image = cls.get_image(request.virtual_machine_image, request.tenant_id)
		image.validate_compatibility(request.disk_mib)
		server = PlacementService().select_server(request, image.platform)
		virtual_machine = cls.insert_draft(request, cast(str, image.name), cast(str, server.name))
		service = cls(virtual_machine)
		server_ip_address = (
			service.assign_ip_address(request.server_ip_address) if request.server_ip_address else None
		)
		metal_request = service.get_metal_request(request, image, server_ip_address)
		frappe.db.commit()  # nosemgrep

		virtual_machine_name = cast(str, virtual_machine.name)
		try:
			MetalClient(server).put_virtual_machine(virtual_machine_name, metal_request)
		except MetalClientError as error:
			if error.uncertain:
				return {"name": virtual_machine_name, "is_draft": True}
			raise VirtualMachineCreateError(virtual_machine_name, error) from error

		virtual_machine.is_draft = 0
		virtual_machine.save()
		return {"name": virtual_machine_name, "is_draft": False}

	@staticmethod
	def get_image(image_name: str, tenant_id: int) -> VirtualMachineImage:
		"""Return one enabled and available virtual machine image."""
		image = cast("VirtualMachineImage", frappe.get_doc("Virtual Machine Image", image_name))
		if not image.is_visible_to_tenant(tenant_id):
			frappe.throw(
				_("Virtual Machine Image {0} is not available.").format(image.title),
				exc=frappe.DoesNotExistError,
			)
		if not image.enabled:
			frappe.throw(_("Virtual Machine Image {0} is disabled.").format(image.title), exc=AtlasUserError)
		image.validate_is_available()
		return image

	@staticmethod
	def insert_draft(
		request: VirtualMachineCreateRequest, image_name: str, server_name: str
	) -> VirtualMachine:
		"""Insert one draft that reserves capacity before a Metal request."""
		virtual_machine = frappe.get_doc(
			{
				"doctype": "Virtual Machine",
				"is_draft": 1,
				"server": server_name,
				"virtual_machine_image": image_name,
				"vcpus": request.virtual_cpu_count,
				"memory_mib": request.memory_mib,
				"disk_mib": request.disk_mib,
				"tenant_id": request.tenant_id,
				"is_privileged": request.is_privileged,
				"sleep_after_idle_seconds": request.sleep_after_idle_seconds,
			}
		)
		virtual_machine.flags.created_by_virtual_machine_api = True
		return cast("VirtualMachine", virtual_machine.insert())

	def get_metal_request(
		self,
		request: VirtualMachineCreateRequest,
		image: VirtualMachineImage,
		server_ip_address: MetalServerIPAddress | None,
	) -> dict[str, Any]:
		"""Return the complete Metal create request."""
		return {
			"compute": {
				"virtual_cpu_count": request.virtual_cpu_count,
				"memory_mib": request.memory_mib,
				"sleep_after_idle_seconds": request.sleep_after_idle_seconds,
			},
			"disk": {
				"size_mib": request.disk_mib,
				"throughput_mibps": request.disk_throughput_mibps,
				"iops": request.disk_iops,
			},
			"image": image.get_metal_image_request(),
			"network": {
				"public_ipv4": server_ip_address.address if server_ip_address else "",
				"wireguard_mesh_ipv6": get_virtual_machine_mesh_address(self.virtual_machine),
				"private_network_throughput_mibps": request.private_network_throughput_mibps,
				"public_network_throughput_mibps": request.public_network_throughput_mibps,
				"egress": request.egress,
			},
			"guest": {
				"hostname": request.hostname,
				"ssh_keys": list(request.ssh_keys),
				"metadata": dict(request.metadata),
				"user_data": request.user_data,
			},
		}

	def get_information(self) -> MetalVirtualMachine | None:
		"""Return Metal state, or no value when the virtual machine is absent."""
		try:
			return self.metal_client.get_virtual_machine(cast(str, self.virtual_machine.name))
		except MetalClientError as error:
			if error.is_not_found:
				return None
			self.raise_metal_error(error)

	def validate_deletion(self) -> None:
		"""Allow deletion only after Metal confirms absence."""
		is_absence_confirmed = getattr(self.virtual_machine.flags, "metal_absence_confirmed", False)
		if self.virtual_machine.is_draft and not is_absence_confirmed:
			frappe.throw(
				_("Wait for Virtual Machine creation reconciliation before deletion."),
				exc=AtlasUserError,
			)

		if not is_absence_confirmed:
			try:
				self.metal_client.get_virtual_machine(cast(str, self.virtual_machine.name))
			except MetalClientError as error:
				if not error.is_not_found:
					self.raise_metal_error(error)
			else:
				frappe.throw(
					_("Terminate Virtual Machine {0} before deletion.").format(self.virtual_machine.name),
					exc=AtlasUserError,
				)
		self.release_ip_address()

	def terminate(self) -> None:
		"""Request deletion in Metal, then release the public address intent."""
		try:
			self.metal_client.delete_virtual_machine(cast(str, self.virtual_machine.name))
		except MetalClientError as error:
			if not error.is_not_found:
				self.raise_metal_error(error)

		self.virtual_machine.db_set("is_terminating", 1)
		self.release_ip_address()

	def replace_ssh_keys(self, ssh_keys: list[str]) -> dict[str, Any]:
		"""Replace all desired SSH keys in Metal."""
		information = self.perform_metal_operation(
			lambda metal_client: metal_client.replace_virtual_machine_ssh_keys(
				cast(str, self.virtual_machine.name), ssh_keys
			)
		)
		return information.as_dict()

	def replace_metadata(self, metadata: dict[str, str]) -> dict[str, Any]:
		"""Replace all desired guest metadata in Metal."""
		information = self.perform_metal_operation(
			lambda metal_client: metal_client.replace_virtual_machine_metadata(
				cast(str, self.virtual_machine.name), metadata
			)
		)
		return information.as_dict()

	def set_power_state(self, state: str) -> None:
		"""Set the desired power state in Metal."""
		self.perform_metal_operation(
			lambda metal_client: metal_client.set_virtual_machine_power_state(
				cast(str, self.virtual_machine.name), state
			)
		)

	def request_restart(self) -> None:
		"""Request one restart in Metal."""
		self.perform_metal_operation(
			lambda metal_client: metal_client.request_virtual_machine_restart(
				cast(str, self.virtual_machine.name)
			)
		)

	def set_compute(self, compute: dict[str, Any]) -> dict[str, Any]:
		"""Set the complete compute values in Metal."""
		information = self.perform_metal_operation(
			lambda metal_client: metal_client.set_virtual_machine_compute(
				cast(str, self.virtual_machine.name), compute
			)
		)
		return information.as_dict()

	def update_compute(self, changes: dict[str, Any]) -> dict[str, Any]:
		"""Apply selected compute changes."""
		information = self.require_information()
		current_compute = information.desired.compute
		request = {
			"virtual_cpu_count": current_compute.virtual_cpu_count,
			"memory_mib": current_compute.memory_mib,
			"sleep_after_idle_seconds": self.virtual_machine.sleep_after_idle_seconds,
			**changes,
		}
		is_shape_changed = (
			request["virtual_cpu_count"] != current_compute.virtual_cpu_count
			or request["memory_mib"] != current_compute.memory_mib
		)
		if is_shape_changed and information.observed.state != "stopped":
			frappe.throw(
				_("Stop the Virtual Machine before you change its compute values."), exc=AtlasUserError
			)

		result = self.set_compute(request)
		if is_shape_changed:
			self.virtual_machine.db_set(
				{"vcpus": request["virtual_cpu_count"], "memory_mib": request["memory_mib"]}
			)
		if request["sleep_after_idle_seconds"] != self.virtual_machine.sleep_after_idle_seconds:
			self.virtual_machine.db_set("sleep_after_idle_seconds", request["sleep_after_idle_seconds"])

		return result

	def set_disk(self, size_mib: int, throughput_mibps: int, iops: int) -> dict[str, Any]:
		"""Set the complete disk values in Metal."""
		information = self.perform_metal_operation(
			lambda metal_client: metal_client.set_virtual_machine_disk(
				cast(str, self.virtual_machine.name),
				{"size_mib": size_mib, "throughput_mibps": throughput_mibps, "iops": iops},
			)
		)
		return information.as_dict()

	def update_disk(self, changes: dict[str, int]) -> dict[str, Any]:
		"""Apply selected disk changes and keep the stored disk size in step."""
		current_disk = self.require_information().desired.disk
		size_mib = changes.get("size_mib", current_disk.size_mib)
		if size_mib < current_disk.size_mib:
			frappe.throw(_("Disk size can only increase."), exc=AtlasUserError)

		information = self.set_disk(
			size_mib,
			changes.get("throughput_mibps", current_disk.throughput_mibps),
			changes.get("iops", current_disk.iops),
		)
		if size_mib != current_disk.size_mib:
			self.virtual_machine.db_set("disk_mib", size_mib)

		return information

	def update_network(self, changes: dict[str, Any]) -> dict[str, Any]:
		"""Replace the complete network after applying selected changes."""
		current_network = self.require_information().desired.network
		request = {
			"egress": current_network.egress,
			"public_ipv4": current_network.public_ipv4,
			"wireguard_mesh_ipv6": current_network.wireguard_mesh_ipv6,
			"private_network_throughput_mibps": current_network.private_network_throughput_mibps,
			"public_network_throughput_mibps": current_network.public_network_throughput_mibps,
			**changes,
		}
		information = self.perform_metal_operation(
			lambda metal_client: metal_client.set_virtual_machine_network(
				cast(str, self.virtual_machine.name), request
			)
		)
		return information.as_dict()

	def apply_network_changes(self, changes: dict[str, Any]) -> dict[str, Any]:
		"""Apply selected network changes after the egress and address rules."""
		self.virtual_machine.validate_network_change()
		egress = changes.get("egress")
		if egress is not None and egress not in EGRESS_MODES:
			frappe.throw(_("Egress must be uplink, mesh, or none."), exc=AtlasUserError)
		if egress not in (None, "uplink") and self.get_attached_ip_address_name():
			frappe.throw(
				_("Detach the public IPv4 address before you remove the internet path."),
				exc=AtlasUserError,
			)

		return self.update_network(changes)

	def attach_ip_address(self, server_ip_address: str) -> dict[str, Any]:
		"""Set the address intent before the Metal network request."""
		address = self.assign_ip_address(server_ip_address)
		return self.update_network({"egress": "uplink", "public_ipv4": address.address})

	def detach_ip_address(self) -> dict[str, Any]:
		"""Update Metal before the address release intent."""
		information = self.update_network({"public_ipv4": ""})
		self.release_ip_address()
		return information

	def assign_ip_address(self, server_ip_address: str) -> MetalServerIPAddress:
		"""Set an attach intent for one reserved public address."""
		address = cast(
			"MetalServerIPAddress",
			frappe.get_doc("Metal Server IP Address", server_ip_address, for_update=True),
		)
		if address.tenant_id != self.virtual_machine.tenant_id:
			frappe.throw(_("The IP address belongs to another tenant."), exc=frappe.PermissionError)
		if address.status != "Allocated" or address.virtual_machine:
			frappe.throw(_("The IP address is not available."), exc=frappe.ValidationError)
		address.begin_assignment(self.virtual_machine.server, self.virtual_machine.name)
		return address

	def release_ip_address(self) -> None:
		"""Set a release intent for the assigned public address."""
		address_name = self.get_attached_ip_address_name()
		if address_name:
			frappe.get_doc("Metal Server IP Address", address_name).release()

	def get_attached_ip_address_name(self) -> str | None:
		"""Return the public address this virtual machine holds, when there is one."""
		return frappe.db.get_value("Metal Server IP Address", {"virtual_machine": self.virtual_machine.name})

	def require_information(self) -> MetalVirtualMachine:
		"""Return Metal state or raise a Frappe error."""
		return self.perform_metal_operation(
			lambda metal_client: metal_client.get_virtual_machine(cast(str, self.virtual_machine.name))
		)

	def get_console_connection(self, mode: str) -> dict[str, str]:
		"""Return the Metal console connection values."""
		return self.perform_metal_operation(
			lambda metal_client: metal_client.get_console_connection(
				cast(str, self.virtual_machine.name), mode
			)
		)

	def perform_metal_operation[Result](self, operation: Callable[[MetalClient], Result]) -> Result:
		"""Run one Metal operation and translate its error for Frappe."""
		try:
			return operation(self.metal_client)
		except MetalClientError as error:
			self.raise_metal_error(error)

	@property
	def metal_client(self) -> MetalClient:
		"""Return a Metal client for the assigned Server."""
		server = cast("MetalServer", frappe.get_doc("Metal Server", self.virtual_machine.server))
		return MetalClient(server)

	@staticmethod
	def raise_metal_error(error: MetalClientError) -> Never:
		"""Raise one safe Metal failure at the Frappe boundary."""
		frappe.throw(_("Metal request failed: {0}").format(error), exc=MetalOperationError)
		raise AssertionError from error
