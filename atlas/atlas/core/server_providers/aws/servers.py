from __future__ import annotations

import hashlib
from collections.abc import Mapping
from typing import ClassVar

from atlas.atlas.core.server_providers.aws.catalog import AwsCatalog
from atlas.atlas.core.server_providers.aws.client import AwsClient, AwsError
from atlas.atlas.core.server_providers.aws.configuration import AwsConfiguration
from atlas.atlas.core.server_providers.base import (
	ProviderServer,
	ServerCreateRequest,
	ServerPowerAction,
)

MESH_MULTICAST_GROUP = "239.1.1.1"


class AwsServers:
	"""Own AWS Elastic Compute Cloud instance operations."""

	server_status_map: ClassVar[dict[str, str]] = {
		"pending": "Installing",
		"running": "Installing",
		"stopping": "Stopped",
		"stopped": "Stopped",
		"shutting-down": "Deleted",
		"terminated": "Deleted",
	}
	power_operation_map: ClassVar[dict[ServerPowerAction, str]] = {
		ServerPowerAction.REBOOT: "reboot_instances",
		ServerPowerAction.START: "start_instances",
		ServerPowerAction.STOP: "stop_instances",
	}
	live_states: ClassVar[frozenset[str]] = frozenset({"pending", "running", "stopping", "stopped"})
	identity_tag_key: ClassVar[str] = "atlas-server"

	def __init__(self, client: AwsClient, configuration: AwsConfiguration, catalog: AwsCatalog) -> None:
		self.client = client
		self.configuration = configuration
		self.catalog = catalog

	def ensure(self, request: ServerCreateRequest) -> ProviderServer:
		"""Return the named instance, and create it when it does not exist."""
		instance = self.find(request.name)
		was_created = instance is None
		if instance is None:
			instance = self.create(request)

		return self.to_provider_server(instance, was_created=was_created)

	def create(self, request: ServerCreateRequest) -> Mapping:
		"""Create one AWS instance from an Atlas request."""
		if (
			not self.configuration.key_pair_name
			or not self.configuration.subnet_id
			or not self.configuration.security_group_id
		):
			raise AwsError("Atlas Settings has no AWS key pair, subnet, or security group")

		response = self.client.call(
			"ec2",
			"run_instances",
			ClientToken=self.client_token("instance", request.name),
			ImageId=self.catalog.image_id(request.image_provider_metadata, request.server_image),
			InstanceType=request.server_size,
			MinCount=1,
			MaxCount=1,
			KeyName=self.configuration.key_pair_name,
			SubnetId=self.configuration.subnet_id,
			SecurityGroupIds=[self.configuration.security_group_id],
			TagSpecifications=[
				{
					"ResourceType": "instance",
					"Tags": [
						{"Key": "Name", "Value": request.name},
						{"Key": self.identity_tag_key, "Value": request.name},
					],
				}
			],
			**self.cpu_options(request.size_provider_metadata),
		)
		instances = response.get("Instances", [])
		if not isinstance(instances, list) or len(instances) != 1 or not isinstance(instances[0], Mapping):
			raise AwsError(f"AWS did not return an instance for Atlas server {request.name}")
		return instances[0]

	@staticmethod
	def cpu_options(size_provider_metadata: Mapping) -> dict:
		"""Return the CPU options that let a virtual instance run Atlas guests.

		AWS keeps nested virtualization off until an instance asks for it at launch.
		A bare metal instance already exposes the processor extensions and rejects
		the option.
		"""
		if size_provider_metadata.get("BareMetal"):
			return {}
		return {"CpuOptions": {"NestedVirtualization": "enabled"}}

	def find(self, server_name: str) -> Mapping | None:
		"""Return the instance with the Atlas identity tag."""
		reservations = self.client.paginate(
			"ec2",
			"describe_instances",
			"Reservations",
			Filters=[
				{"Name": f"tag:{self.identity_tag_key}", "Values": [server_name]},
				{"Name": "instance-state-name", "Values": sorted(self.live_states)},
			],
		)
		instances = [instance for reservation in reservations for instance in reservation["Instances"]]
		if len(instances) > 1:
			raise AwsError(f"AWS returned multiple instances for Atlas server {server_name}")
		return instances[0] if instances else None

	def fetch(self, provider_server_id: str) -> Mapping:
		"""Return one AWS instance."""
		response = self.client.call("ec2", "describe_instances", InstanceIds=[provider_server_id])
		for reservation in response.get("Reservations", []):
			for instance in reservation.get("Instances", []):
				return instance
		raise AwsError(f"AWS has no instance {provider_server_id}")

	def is_ready(self, provider_server_id: str) -> bool:
		"""Report whether both AWS status checks pass for one instance."""
		response = self.client.call("ec2", "describe_instance_status", InstanceIds=[provider_server_id])
		for status in response.get("InstanceStatuses", []):
			return (
				status.get("InstanceStatus", {}).get("Status") == "ok"
				and status.get("SystemStatus", {}).get("Status") == "ok"
			)
		return False

	def ensure_mesh_interface(self, provider_server_id: str, server_name: str) -> Mapping:
		"""Return the mesh network interface, and create and attach it when it does not exist."""
		if not self.configuration.subnet_id or not self.configuration.security_group_id:
			raise AwsError("Atlas Settings has no AWS subnet or security group")

		interface = self.find_mesh_interface(server_name)
		if interface is None:
			response = self.client.call(
				"ec2",
				"create_network_interface",
				ClientToken=self.client_token("mesh-interface", server_name),
				SubnetId=self.configuration.subnet_id,
				Groups=[self.configuration.security_group_id],
				Description=f"Atlas mesh interface for {server_name}",
				TagSpecifications=[
					{
						"ResourceType": "network-interface",
						"Tags": [{"Key": self.identity_tag_key, "Value": server_name}],
					}
				],
			)
			interface = response["NetworkInterface"]

		attachment = interface.get("Attachment", {})
		attached_instance = attachment.get("InstanceId")
		if attached_instance and attached_instance != provider_server_id:
			raise AwsError(
				f"AWS mesh interface {interface['NetworkInterfaceId']} belongs to instance {attached_instance}"
			)
		if not attached_instance:
			self.client.call(
				"ec2",
				"attach_network_interface",
				NetworkInterfaceId=interface["NetworkInterfaceId"],
				InstanceId=provider_server_id,
				DeviceIndex=1,
			)
		return interface

	def find_mesh_interface(self, server_name: str) -> Mapping | None:
		"""Return the mesh interface with the Atlas server identity tag."""
		interfaces = self.client.paginate(
			"ec2",
			"describe_network_interfaces",
			"NetworkInterfaces",
			Filters=[
				{"Name": f"tag:{self.identity_tag_key}", "Values": [server_name]},
				{"Name": "subnet-id", "Values": [self.configuration.subnet_id]},
			],
		)
		if len(interfaces) > 1:
			raise AwsError(f"AWS returned multiple mesh interfaces for Atlas server {server_name}")
		return interfaces[0] if interfaces else None

	def fetch_mesh_interface(self, network_interface_id: str) -> Mapping:
		"""Return one AWS mesh network interface."""
		response = self.client.call(
			"ec2", "describe_network_interfaces", NetworkInterfaceIds=[network_interface_id]
		)
		interfaces = response.get("NetworkInterfaces", [])
		if len(interfaces) != 1:
			raise AwsError(f"AWS has no mesh interface {network_interface_id}")
		return interfaces[0]

	def configure_mesh_interface(self, interface: Mapping) -> None:
		"""Set the required attributes on an attached mesh interface."""
		interface_id = interface["NetworkInterfaceId"]
		attachment_id = interface.get("Attachment", {}).get("AttachmentId")
		if not isinstance(attachment_id, str):
			raise AwsError(f"AWS mesh interface {interface_id} has no attachment ID")

		self.client.call(
			"ec2",
			"modify_network_interface_attribute",
			NetworkInterfaceId=interface_id,
			Attachment={"AttachmentId": attachment_id, "DeleteOnTermination": True},
		)
		self.client.call(
			"ec2",
			"modify_network_interface_attribute",
			NetworkInterfaceId=interface_id,
			SourceDestCheck={"Value": False},
		)

	def mesh_interface(self, provider_server_id: str) -> Mapping | None:
		"""Return the secondary network interface that carries Atlas mesh traffic."""
		response = self.client.call(
			"ec2",
			"describe_network_interfaces",
			Filters=[{"Name": "attachment.instance-id", "Values": [provider_server_id]}],
		)
		for interface in response.get("NetworkInterfaces", []):
			if interface.get("Attachment", {}).get("DeviceIndex") == 1:
				return interface
		return None

	@staticmethod
	def client_token(kind: str, server_name: str) -> str:
		"""Return a stable AWS idempotency token for one server resource."""
		return hashlib.sha256(f"atlas:{kind}:{server_name}".encode()).hexdigest()

	def register_multicast_interface(self, network_interface_id: str) -> None:
		"""Register one mesh interface as a multicast member and source.

		WG Mesh reads discovery frames with an eBPF hook and never joins the group
		with a socket, so the host sends no IGMP report. Atlas must register the
		interface statically or the transit gateway delivers nothing.
		"""
		if not self.configuration.multicast_domain_id:
			raise AwsError("Atlas Settings has no AWS multicast domain ID")

		for operation in (
			"register_transit_gateway_multicast_group_members",
			"register_transit_gateway_multicast_group_sources",
		):
			self.client.call(
				"ec2",
				operation,
				TransitGatewayMulticastDomainId=self.configuration.multicast_domain_id,
				GroupIpAddress=MESH_MULTICAST_GROUP,
				NetworkInterfaceIds=[network_interface_id],
			)

	def deregister_multicast_interface(self, network_interface_id: str) -> None:
		"""Remove one mesh interface from the multicast domain."""
		if not self.configuration.multicast_domain_id:
			return

		for operation in (
			"deregister_transit_gateway_multicast_group_members",
			"deregister_transit_gateway_multicast_group_sources",
		):
			self.client.call(
				"ec2",
				operation,
				TransitGatewayMulticastDomainId=self.configuration.multicast_domain_id,
				GroupIpAddress=MESH_MULTICAST_GROUP,
				NetworkInterfaceIds=[network_interface_id],
				allow_missing=True,
			)

	def set_power_state(self, provider_server_id: str, action: ServerPowerAction) -> None:
		"""Apply one power action to an AWS instance."""
		self.client.call("ec2", self.power_operation_map[action], InstanceIds=[provider_server_id])

	def delete(self, provider_server_id: str) -> None:
		"""Delete one AWS instance and its mesh interface if they exist."""
		interface = self.mesh_interface(provider_server_id)
		if interface is not None:
			self.deregister_multicast_interface(interface["NetworkInterfaceId"])

		self.client.call("ec2", "terminate_instances", InstanceIds=[provider_server_id], allow_missing=True)

	@classmethod
	def to_provider_server(cls, instance: Mapping, *, was_created: bool = False) -> ProviderServer:
		"""Convert one AWS instance to provider-neutral data."""
		provider_server_id = instance.get("InstanceId")
		if not isinstance(provider_server_id, str):
			raise AwsError("AWS did not return an instance ID")

		public_address = instance.get("PublicIpAddress")
		return ProviderServer(
			provider_server_id=provider_server_id,
			status=cls.server_status_map.get(instance.get("State", {}).get("Name")),
			public_ipv4_address=public_address if isinstance(public_address, str) else None,
			provider_metadata={"instance": dict(instance)},
			was_created=was_created,
		)
