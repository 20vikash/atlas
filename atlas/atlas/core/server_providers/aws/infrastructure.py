from __future__ import annotations

import ipaddress
import re
from pathlib import PurePosixPath
from typing import TYPE_CHECKING

from atlas.atlas.core.server_providers.aws.client import AwsError

if TYPE_CHECKING:
	from atlas.atlas.core.server_providers.aws.provider import AwsProvider

PRIVATE_IPV4_NETWORKS = tuple(
	ipaddress.ip_network(cidr) for cidr in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")
)


class AwsInfrastructure:
	"""Manage the AWS resources shared by Atlas servers."""

	def __init__(self, provider: "AwsProvider") -> None:
		self.provider = provider

	def validate_settings(self) -> None:
		"""Validate the AWS region, zone, and private network settings."""
		configuration = self.provider.configuration
		if not re.fullmatch(rf"{re.escape(configuration.region)}[a-z]", configuration.availability_zone):
			raise AwsError(
				f"AWS Availability Zone {configuration.availability_zone} is not in region {configuration.region}"
			)

		try:
			network = ipaddress.ip_network(self.provider.settings.private_network_cidr, strict=False)
		except ValueError as error:
			raise AwsError(
				f"Invalid Atlas private network CIDR: {self.provider.settings.private_network_cidr}"
			) from error

		if network.version != 4 or not any(network.subnet_of(item) for item in PRIVATE_IPV4_NETWORKS):
			raise AwsError(f"Atlas private network CIDR {network} must be a private IPv4 network")

		if not (
			self.provider.private_network_min_prefix
			<= network.prefixlen
			<= self.provider.private_network_max_prefix
		):
			raise AwsError(
				f"Private network CIDR {network} must have a prefix length between "
				f"/{self.provider.private_network_min_prefix} and /{self.provider.private_network_max_prefix}."
			)

		storage_device = PurePosixPath(configuration.storage_pool_device)
		if (
			not storage_device.is_absolute()
			or not storage_device.is_relative_to("/dev")
			or len(storage_device.parts) < 3
			or ".." in storage_device.parts
		):
			raise AwsError("AWS storage pool device must be a path below /dev")

	def validate_credentials(self) -> bool:
		"""Return true when the configured keys can access AWS."""
		self.provider.client.call("sts", "get_caller_identity")
		return True

	def create_vpc(self) -> str:
		"""Return the Atlas VPC ID, or create an Atlas VPC."""
		cidr = self.private_network_cidr
		existing = self.find_configured_or_named(
			"describe_vpcs",
			"Vpcs",
			"VpcIds",
			self.provider.configuration.vpc_id,
			self.resource_name("vpc"),
		)
		if existing is not None:
			if existing["CidrBlock"] != cidr:
				raise AwsError(f"VPC {existing['VpcId']} does not use CIDR {cidr}")
			vpc_id = existing["VpcId"]
		else:
			response = self.provider.client.call(
				"ec2",
				"create_vpc",
				CidrBlock=cidr,
				TagSpecifications=[self.tag_specification("vpc", "vpc")],
			)
			vpc_id = response["Vpc"]["VpcId"]
		self.provider.client.call(
			"ec2", "modify_vpc_attribute", VpcId=vpc_id, EnableDnsHostnames={"Value": True}
		)
		return vpc_id

	def create_internet_access(self, vpc_id: str) -> None:
		"""Give the Atlas VPC a default route to the internet."""
		gateway = self.find(
			"ec2", "describe_internet_gateways", "InternetGateways", self.resource_name("internet-gateway")
		)
		if gateway is None:
			response = self.provider.client.call(
				"ec2",
				"create_internet_gateway",
				TagSpecifications=[self.tag_specification("internet-gateway", "internet-gateway")],
			)
			gateway = response["InternetGateway"]
		gateway_id = gateway["InternetGatewayId"]

		if not gateway.get("Attachments"):
			self.provider.client.call(
				"ec2", "attach_internet_gateway", InternetGatewayId=gateway_id, VpcId=vpc_id
			)
		elif not any(attachment.get("VpcId") == vpc_id for attachment in gateway["Attachments"]):
			raise AwsError(f"Internet gateway {gateway_id} belongs to another VPC")

		route_table_id, default_gateway_id = self.main_route_table(vpc_id)
		if default_gateway_id and default_gateway_id != gateway_id:
			raise AwsError(f"VPC {vpc_id} has a default route through {default_gateway_id}")
		if not default_gateway_id:
			self.provider.client.call(
				"ec2",
				"create_route",
				RouteTableId=route_table_id,
				DestinationCidrBlock="0.0.0.0/0",
				GatewayId=gateway_id,
			)

	def create_subnet(self, vpc_id: str) -> str:
		"""Return the Atlas subnet ID, or create the single Atlas subnet.

		Atlas uses one subnet for the whole region. WG Mesh discovery uses a
		multicast time to live of 1, so every host must share one subnet.
		"""
		existing = self.find_configured_or_named(
			"describe_subnets",
			"Subnets",
			"SubnetIds",
			self.provider.configuration.subnet_id,
			self.resource_name("subnet"),
		)
		if existing is not None:
			if existing["VpcId"] != vpc_id:
				raise AwsError(f"Subnet {existing['SubnetId']} does not belong to VPC {vpc_id}")
			if existing.get("CidrBlock") != self.private_network_cidr:
				raise AwsError(f"Subnet {existing['SubnetId']} does not use the Atlas private network")
			if existing.get("AvailabilityZone") != self.provider.configuration.availability_zone:
				raise AwsError(f"Subnet {existing['SubnetId']} is in another Availability Zone")
			subnet_id = existing["SubnetId"]
		else:
			response = self.provider.client.call(
				"ec2",
				"create_subnet",
				VpcId=vpc_id,
				CidrBlock=self.private_network_cidr,
				AvailabilityZone=self.provider.configuration.availability_zone,
				TagSpecifications=[self.tag_specification("subnet", "subnet")],
			)
			subnet_id = response["Subnet"]["SubnetId"]
		self.provider.client.call(
			"ec2",
			"modify_subnet_attribute",
			SubnetId=subnet_id,
			MapPublicIpOnLaunch={"Value": True},
		)
		return subnet_id

	def create_security_group(self, vpc_id: str) -> str:
		"""Return the Atlas security group ID, or create the group."""
		existing = self.find_configured_or_named(
			"describe_security_groups",
			"SecurityGroups",
			"GroupIds",
			self.provider.configuration.security_group_id,
			self.resource_name("security-group"),
		)
		if existing is not None:
			if existing.get("VpcId") != vpc_id:
				raise AwsError(f"Security group {existing['GroupId']} does not belong to VPC {vpc_id}")
			group_id = existing["GroupId"]
			permissions = existing.get("IpPermissions", [])
		else:
			response = self.provider.client.call(
				"ec2",
				"create_security_group",
				GroupName=self.resource_name("security-group"),
				Description="Atlas host traffic",
				VpcId=vpc_id,
				TagSpecifications=[self.tag_specification("security-group", "security-group")],
			)
			group_id = response["GroupId"]
			permissions = []

		missing_rules = [rule for rule in self.ingress_rules if not self.has_ingress_rule(permissions, rule)]
		if missing_rules:
			self.provider.client.call(
				"ec2", "authorize_security_group_ingress", GroupId=group_id, IpPermissions=missing_rules
			)
		return group_id

	def create_key_pair(self, public_key: str) -> str:
		"""Return the Atlas key pair name, or import the Atlas public key."""
		name = self.resource_name("ssh-key")
		existing = self.provider.client.call("ec2", "describe_key_pairs", KeyNames=[name], allow_missing=True)
		if existing.get("KeyPairs"):
			return name

		self.provider.client.call(
			"ec2",
			"import_key_pair",
			KeyName=name,
			PublicKeyMaterial=public_key.encode(),
			TagSpecifications=[self.tag_specification("key-pair", "ssh-key")],
		)
		return name

	def create_transit_gateway(self) -> str:
		"""Return the Atlas transit gateway ID, or create a multicast transit gateway."""
		existing = self.find_configured_or_named(
			"describe_transit_gateways",
			"TransitGateways",
			"TransitGatewayIds",
			self.provider.configuration.transit_gateway_id,
			self.resource_name("transit-gateway"),
			ignored_states=("deleted", "deleting"),
		)
		if existing is not None:
			if existing.get("Options", {}).get("MulticastSupport") != "enable":
				raise AwsError("AWS transit gateway does not have multicast support")
			return existing["TransitGatewayId"]

		response = self.provider.client.call(
			"ec2",
			"create_transit_gateway",
			Description="Atlas WG Mesh discovery",
			Options={"MulticastSupport": "enable", "DefaultRouteTableAssociation": "enable"},
			TagSpecifications=[self.tag_specification("transit-gateway", "transit-gateway")],
		)
		return response["TransitGateway"]["TransitGatewayId"]

	def attach_transit_gateway(self, transit_gateway_id: str, vpc_id: str, subnet_id: str) -> str:
		"""Return the Atlas transit gateway attachment ID, or attach the Atlas VPC."""
		existing = self.find_configured_or_named(
			"describe_transit_gateway_vpc_attachments",
			"TransitGatewayVpcAttachments",
			"TransitGatewayAttachmentIds",
			self.provider.configuration.transit_gateway_attachment_id,
			self.resource_name("transit-gateway-attachment"),
			ignored_states=("deleted", "deleting"),
		)
		if existing is not None:
			if existing.get("TransitGatewayId") != transit_gateway_id:
				raise AwsError("AWS transit gateway attachment belongs to another transit gateway")
			if existing.get("VpcId") != vpc_id or subnet_id not in existing.get("SubnetIds", []):
				raise AwsError("AWS transit gateway attachment does not use the Atlas VPC and subnet")
			return existing["TransitGatewayAttachmentId"]

		response = self.provider.client.call(
			"ec2",
			"create_transit_gateway_vpc_attachment",
			TransitGatewayId=transit_gateway_id,
			VpcId=vpc_id,
			SubnetIds=[subnet_id],
			TagSpecifications=[
				self.tag_specification("transit-gateway-attachment", "transit-gateway-attachment")
			],
		)
		return response["TransitGatewayVpcAttachment"]["TransitGatewayAttachmentId"]

	def create_multicast_domain(self, transit_gateway_id: str) -> str:
		"""Return the Atlas multicast domain ID, or create a static multicast domain.

		WG Mesh reads discovery frames with an eBPF hook and never joins the group
		with a socket, so the host sends no IGMP report. The domain must therefore
		use static sources and static members.
		"""
		existing = self.find_configured_or_named(
			"describe_transit_gateway_multicast_domains",
			"TransitGatewayMulticastDomains",
			"TransitGatewayMulticastDomainIds",
			self.provider.configuration.multicast_domain_id,
			self.resource_name("multicast-domain"),
			ignored_states=("deleted", "deleting"),
		)
		if existing is not None:
			if existing.get("TransitGatewayId") != transit_gateway_id:
				raise AwsError("AWS multicast domain belongs to another transit gateway")
			options = existing.get("Options", {})
			if options.get("Igmpv2Support") != "disable" or options.get("StaticSourcesSupport") != "enable":
				raise AwsError("AWS multicast domain does not use static membership")
			return existing["TransitGatewayMulticastDomainId"]

		response = self.provider.client.call(
			"ec2",
			"create_transit_gateway_multicast_domain",
			TransitGatewayId=transit_gateway_id,
			Options={
				"Igmpv2Support": "disable",
				"StaticSourcesSupport": "enable",
				"AutoAcceptSharedAssociations": "disable",
			},
			TagSpecifications=[
				self.tag_specification("transit-gateway-multicast-domain", "multicast-domain")
			],
		)
		return response["TransitGatewayMulticastDomain"]["TransitGatewayMulticastDomainId"]

	def associate_multicast_subnet(self, domain_id: str, attachment_id: str, subnet_id: str) -> None:
		"""Associate the Atlas subnet with the multicast domain."""
		associations = self.provider.client.call(
			"ec2",
			"get_transit_gateway_multicast_domain_associations",
			TransitGatewayMulticastDomainId=domain_id,
		)
		for association in associations.get("MulticastDomainAssociations", []):
			if association.get("Subnet", {}).get("SubnetId") == subnet_id:
				return

		self.provider.client.call(
			"ec2",
			"associate_transit_gateway_multicast_domain",
			TransitGatewayMulticastDomainId=domain_id,
			TransitGatewayAttachmentId=attachment_id,
			SubnetIds=[subnet_id],
		)

	def wait_for_available(self, operation: str, key: str, description: str, **parameters: object) -> None:
		"""Poll one AWS resource until it is available."""

		def is_available() -> bool | None:
			"""Report whether the resource became available."""
			items = self.provider.client.call("ec2", operation, **parameters).get(key, [])
			if not items:
				raise AwsError(f"AWS did not return {description}")
			state = items[0]["State"]
			if state == "available":
				return True
			if state not in {"pending", "modifying", "initiating", "initiatingRequest"}:
				raise AwsError(f"AWS reported state {state} for {description}")
			return None

		self.provider.poll(
			is_available,
			timeout_seconds=self.provider.setup_poll_timeout_seconds,
			poll_interval_seconds=self.provider.setup_poll_interval_seconds,
			description=description,
		)

	def find(
		self,
		service: str,
		operation: str,
		key: str,
		name: str,
		*,
		ignored_states: tuple[str, ...] = (),
	) -> dict | None:
		"""Return the single AWS resource that carries one Atlas name tag."""
		response = self.provider.client.call(
			service, operation, Filters=[{"Name": "tag:Name", "Values": [name]}]
		)
		items = [item for item in response.get(key, []) if item.get("State") not in ignored_states]
		if len(items) > 1:
			raise AwsError(f"AWS returned multiple resources named {name}")
		return items[0] if items else None

	def find_configured_or_named(
		self,
		operation: str,
		key: str,
		ids_parameter: str,
		configured_id: str | None,
		name: str,
		*,
		ignored_states: tuple[str, ...] = (),
	) -> dict | None:
		"""Return a configured AWS resource, or find it by its Atlas name."""
		if not configured_id:
			return self.find("ec2", operation, key, name, ignored_states=ignored_states)

		response = self.provider.client.call("ec2", operation, **{ids_parameter: [configured_id]})
		items = [item for item in response.get(key, []) if item.get("State") not in ignored_states]
		if len(items) != 1:
			raise AwsError(f"AWS did not return configured resource {configured_id}")
		return items[0]

	def main_route_table(self, vpc_id: str) -> tuple[str, str | None]:
		"""Return the main route table and the gateway for its default route."""
		response = self.provider.client.call(
			"ec2",
			"describe_route_tables",
			Filters=[
				{"Name": "vpc-id", "Values": [vpc_id]},
				{"Name": "association.main", "Values": ["true"]},
			],
		)
		tables = response.get("RouteTables", [])
		if not tables:
			raise AwsError(f"VPC {vpc_id} has no main route table")
		default_route = next(
			(
				route
				for route in tables[0].get("Routes", [])
				if route.get("DestinationCidrBlock") == "0.0.0.0/0"
			),
			None,
		)
		return tables[0]["RouteTableId"], default_route.get("GatewayId") if default_route else None

	@property
	def private_network_cidr(self) -> str:
		"""Return the canonical Atlas private network CIDR."""
		return str(ipaddress.ip_network(self.provider.settings.private_network_cidr, strict=False))

	@staticmethod
	def has_ingress_rule(permissions: object, expected: dict) -> bool:
		"""Report whether AWS returned one required ingress rule."""
		if not isinstance(permissions, list):
			return False
		expected_cidrs = {item["CidrIp"] for item in expected.get("IpRanges", [])}
		for permission in permissions:
			if not isinstance(permission, dict):
				continue
			actual_cidrs = {item.get("CidrIp") for item in permission.get("IpRanges", [])}
			if (
				permission.get("IpProtocol") == expected.get("IpProtocol")
				and permission.get("FromPort") == expected.get("FromPort")
				and permission.get("ToPort") == expected.get("ToPort")
				and expected_cidrs <= actual_cidrs
			):
				return True
		return False

	def resource_name(self, kind: str) -> str:
		"""Return the Atlas name for one shared AWS resource."""
		return f"{self.provider.configuration.resource_name_prefix}{kind}"

	def tag_specification(self, resource_type: str, kind: str) -> dict:
		"""Return the Atlas name tag for one new AWS resource."""
		return {
			"ResourceType": resource_type,
			"Tags": [{"Key": "Name", "Value": self.resource_name(kind)}],
		}

	@property
	def ingress_rules(self) -> list[dict]:
		"""Return the inbound rules for Atlas hosts.

		Atlas reaches Secure Shell and the Metal API over the public address of a
		host, so both ports are open to the internet. The Metal API uses a bearer
		token. Everything else stays inside the Atlas private network.
		"""
		from atlas.vm.core.metal_client import MetalClient

		return [
			self.port_rule("tcp", 22, "Atlas Secure Shell"),
			self.port_rule("tcp", MetalClient.api_port, "Atlas Metal API"),
			self.port_rule("udp", self.provider.wireguard_port, "WireGuard"),
			{
				"IpProtocol": "-1",
				"IpRanges": [
					{
						"CidrIp": self.private_network_cidr,
						"Description": "Atlas private network",
					}
				],
			},
		]

	@staticmethod
	def port_rule(protocol: str, port: int, description: str) -> dict:
		"""Return one inbound rule that opens a single port to the internet."""
		return {
			"IpProtocol": protocol,
			"FromPort": port,
			"ToPort": port,
			"IpRanges": [{"CidrIp": "0.0.0.0/0", "Description": description}],
		}
