from __future__ import annotations

import re
from collections.abc import Mapping

from atlas.atlas.core.server_providers.aws.client import AwsError
from atlas.atlas.core.server_providers.base import (
	ACCEPTED_OS_VERSIONS,
	ServerImageData,
	ServerSizeData,
)

IMAGE_OWNERS: Mapping[str, str] = {"Ubuntu": "099720109477", "Debian": "136693071363"}

UBUNTU_IMAGE_NAME = re.compile(r"ubuntu/images/hvm-ssd(?:-gp3)?/ubuntu-\w+-(\d\d\.\d\d)-amd64-server-")
DEBIAN_IMAGE_NAME = re.compile(r"debian-(\d+)-amd64-")


NESTED_VIRTUALIZATION_FEATURE = "nested-virtualization"
ARCHITECTURES: Mapping[str, str] = {"x86_64": "amd64", "arm64": "arm64"}


class AwsCatalog:
	"""Translate AWS catalog records into Atlas catalog values."""

	def get_server_sizes(self, instance_types: object) -> tuple[ServerSizeData, ...]:
		"""Return Atlas sizes for the AWS instance types that can run virtual machines."""
		if not isinstance(instance_types, list) or not all(
			isinstance(item, Mapping) for item in instance_types
		):
			raise AwsError("AWS response has invalid instance types")

		return tuple(
			self._server_size(instance_type)
			for instance_type in instance_types
			if self.is_supported(instance_type)
		)

	def get_server_images(self, images: object) -> tuple[ServerImageData, ...]:
		"""Return Atlas images for the newest supported AWS machine image of each version."""
		if not isinstance(images, list) or not all(isinstance(image, Mapping) for image in images):
			raise AwsError("AWS response has invalid machine images")

		newest: dict[str, tuple[str, ServerImageData]] = {}
		for image in images:
			accepted = self._accepted_image(image)
			if accepted is None:
				continue
			creation_date = str(image.get("CreationDate") or "")
			current = newest.get(accepted.image)
			if current is None or creation_date > current[0]:
				newest[accepted.image] = (creation_date, accepted)
		return tuple(image for _, image in newest.values())

	@staticmethod
	def is_supported(instance_type: Mapping) -> bool:
		"""Report whether an AWS instance type can run Atlas virtual machines.

		Atlas needs hardware virtualization, local storage, and a second network
		interface. Bare metal always gives hardware virtualization. A virtual
		instance gives it only when AWS reports the nested virtualization feature.
		"""
		processor = instance_type.get("ProcessorInfo")
		features = processor.get("SupportedFeatures", []) if isinstance(processor, Mapping) else []
		network = instance_type.get("NetworkInfo")
		maximum_interfaces = network.get("MaximumNetworkInterfaces", 0) if isinstance(network, Mapping) else 0

		has_virtualization = bool(instance_type.get("BareMetal")) or (
			NESTED_VIRTUALIZATION_FEATURE in features
		)
		return (
			has_virtualization
			and AwsCatalog._disk_gib(instance_type) > 0
			and isinstance(maximum_interfaces, int)
			and maximum_interfaces >= 2
		)

	@staticmethod
	def image_id(metadata: object, image_name: str) -> str:
		"""Return the AWS machine image ID from provider image metadata."""
		image_id = metadata.get("ImageId") if isinstance(metadata, Mapping) else None
		if not isinstance(image_id, str):
			raise AwsError(f"Metal Server Image {image_name} has no AWS machine image ID")
		return image_id

	def _server_size(self, instance_type: Mapping) -> ServerSizeData:
		name = instance_type.get("InstanceType")
		if not isinstance(name, str):
			raise AwsError("AWS instance type has no name")
		processor = instance_type.get("VCpuInfo")
		memory = instance_type.get("MemoryInfo")
		cpu_count = processor.get("DefaultVCpus") if isinstance(processor, Mapping) else None
		memory_mib = memory.get("SizeInMiB") if isinstance(memory, Mapping) else None
		if not isinstance(cpu_count, int) or not isinstance(memory_mib, int):
			raise AwsError(f"AWS instance type {name} has invalid CPU or memory data")

		return ServerSizeData(
			size=name,
			architecture=self.instance_type_architecture(instance_type),
			cpu_count=cpu_count,
			memory_mib=memory_mib,
			disk_gib=self._disk_gib(instance_type),
			hourly_pricing_usd_cents=None,
			monthly_pricing_usd_cents=None,
			provider_metadata=dict(instance_type),
		)

	@staticmethod
	def instance_type_architecture(instance_type: Mapping) -> str:
		"""Return the Atlas architecture reported by one AWS instance type.

		AWS lists every architecture a type can boot, so a 64-bit type also reports i386."""
		name = instance_type.get("InstanceType")
		processor = instance_type.get("ProcessorInfo")
		supported = processor.get("SupportedArchitectures") if isinstance(processor, Mapping) else None
		if not isinstance(supported, list) or not supported:
			raise AwsError(f"AWS instance type {name} has no supported architectures")

		architectures = {ARCHITECTURES[value] for value in supported if value in ARCHITECTURES}
		if len(architectures) != 1:
			raise AwsError(f"AWS instance type {name} has no single Atlas architecture")
		return architectures.pop()

	@staticmethod
	def _disk_gib(instance_type: Mapping) -> int:
		"""Return the instance store size, which Atlas uses for the storage pool."""
		storage = instance_type.get("InstanceStorageInfo")
		disk_gib = storage.get("TotalSizeInGB", 0) if isinstance(storage, Mapping) else 0
		return disk_gib if isinstance(disk_gib, int) else 0

	@staticmethod
	def _accepted_image(image: Mapping) -> ServerImageData | None:
		name = image.get("Name")
		if not isinstance(name, str):
			return None

		ubuntu = UBUNTU_IMAGE_NAME.match(name)
		debian = DEBIAN_IMAGE_NAME.match(name)
		if ubuntu:
			os_name, version = "Ubuntu", ubuntu.group(1)
		elif debian:
			os_name, version = "Debian", debian.group(1)
		else:
			return None

		if version not in ACCEPTED_OS_VERSIONS.get(os_name, ()):
			return None

		return ServerImageData(
			image=f"{os_name}_{version}",
			os=os_name,
			version=version,
			provider_metadata=dict(image),
		)
