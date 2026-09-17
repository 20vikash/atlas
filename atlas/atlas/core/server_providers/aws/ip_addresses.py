from __future__ import annotations

from atlas.atlas.core.server_providers.aws.client import AwsClient, AwsError
from atlas.atlas.core.server_providers.aws.configuration import AwsConfiguration
from atlas.atlas.core.server_providers.base import ReservedIPAddress


class AwsIPAddresses:
	"""Own AWS Elastic IP address operations."""

	def __init__(self, client: AwsClient, configuration: AwsConfiguration) -> None:
		self.client = client
		self.configuration = configuration

	def reserve(self) -> ReservedIPAddress:
		"""Reserve one public IPv4 address."""
		response = self.client.call(
			"ec2",
			"allocate_address",
			Domain="vpc",
			TagSpecifications=[
				{
					"ResourceType": "elastic-ip",
					"Tags": [{"Key": "Name", "Value": f"{self.configuration.resource_name_prefix}address"}],
				}
			],
		)
		address = response.get("PublicIp")
		provider_resource_id = response.get("AllocationId")
		if not isinstance(address, str) or not isinstance(provider_resource_id, str):
			raise AwsError("AWS did not return an Elastic IP address and allocation ID")

		return ReservedIPAddress(address=address, provider_resource_id=provider_resource_id)

	def delete(self, provider_resource_id: str) -> None:
		"""Release one public IPv4 address if it exists."""
		self.client.call("ec2", "release_address", AllocationId=provider_resource_id, allow_missing=True)

	def attach(self, provider_resource_id: str, provider_server_id: str) -> None:
		"""Attach one public IPv4 address to a provider server."""
		self.client.call(
			"ec2",
			"associate_address",
			AllocationId=provider_resource_id,
			InstanceId=provider_server_id,
			AllowReassociation=False,
		)

	def detach(self, provider_resource_id: str) -> None:
		"""Detach one public IPv4 address from its provider server."""
		association_id = self.association_id(provider_resource_id)
		if association_id is None:
			return
		self.client.call("ec2", "disassociate_address", AssociationId=association_id, allow_missing=True)

	def association_id(self, provider_resource_id: str) -> str | None:
		"""Return the current association of one reserved address."""
		response = self.client.call(
			"ec2", "describe_addresses", AllocationIds=[provider_resource_id], allow_missing=True
		)
		for address in response.get("Addresses", []):
			return address.get("AssociationId")
		return None
