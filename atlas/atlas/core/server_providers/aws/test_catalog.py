from __future__ import annotations

from frappe.tests import UnitTestCase

from atlas.atlas.core.server_providers.aws.catalog import AwsCatalog
from atlas.atlas.core.server_providers.aws.client import AwsError


class TestAwsCatalog(UnitTestCase):
	def test_bare_metal_and_nested_virtualization_types_become_sizes(self) -> None:
		sizes = AwsCatalog().get_server_sizes(
			[
				self.instance_type("c6i.metal", bare_metal=True),
				self.instance_type("m8i.2xlarge", nested_virtualization=True),
				self.instance_type("t3.micro"),
			]
		)

		self.assertEqual([size.size for size in sizes], ["c6i.metal", "m8i.2xlarge"])

	def test_types_without_local_storage_or_a_mesh_interface_are_skipped(self) -> None:
		without_storage = self.instance_type("m8i.2xlarge", nested_virtualization=True)
		without_storage["InstanceStorageInfo"] = {"TotalSizeInGB": 0}
		without_mesh_interface = self.instance_type("m8i.2xlarge", nested_virtualization=True)
		without_mesh_interface["NetworkInfo"] = {"MaximumNetworkInterfaces": 1}

		sizes = AwsCatalog().get_server_sizes([without_storage, without_mesh_interface])

		self.assertEqual(sizes, ())

	def test_size_uses_binary_memory_and_instance_storage(self) -> None:
		catalog = AwsCatalog()

		size = catalog.get_server_sizes([self.instance_type("c6i.metal", bare_metal=True)])[0]

		self.assertEqual(size.cpu_count, 128)
		self.assertEqual(size.memory_mib, 262_144)
		self.assertEqual(size.disk_gib, 7_600)
		self.assertIsNone(size.hourly_pricing_usd_cents)

	def test_the_atlas_architecture_comes_from_the_supported_architectures(self) -> None:
		catalog = AwsCatalog()

		amd64 = catalog.get_server_sizes([self.instance_type("c6i.metal", bare_metal=True)])[0]
		arm64 = catalog.get_server_sizes(
			[self.instance_type("c8g.metal", bare_metal=True, architectures=["arm64"])]
		)[0]

		self.assertEqual(amd64.architecture, "amd64")
		self.assertEqual(arm64.architecture, "arm64")

	def test_an_instance_type_without_an_atlas_architecture_fails_loudly(self) -> None:
		with self.assertRaises(AwsError):
			AwsCatalog().get_server_sizes(
				[self.instance_type("mac1.metal", bare_metal=True, architectures=["x86_64_mac"])]
			)

	def test_invalid_instance_types_fail_loudly(self) -> None:
		with self.assertRaises(AwsError):
			AwsCatalog().get_server_sizes("not-a-list")

	def test_only_accepted_operating_system_versions_become_images(self) -> None:
		images = AwsCatalog().get_server_images(
			[
				self.image("ami-1", "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-20260101"),
				self.image("ami-2", "debian-12-amd64-20260101-1000"),
				self.image("ami-3", "ubuntu/images/hvm-ssd/ubuntu-xenial-16.04-amd64-server-20260101"),
				self.image("ami-4", "amzn2-ami-hvm-2.0-x86_64-gp2"),
			]
		)

		self.assertEqual(sorted(image.image for image in images), ["Debian_12", "Ubuntu_24.04"])

	def test_the_newest_image_of_one_version_wins(self) -> None:
		images = AwsCatalog().get_server_images(
			[
				self.image(
					"ami-old",
					"ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-20260101",
					creation_date="2026-01-01T00:00:00.000Z",
				),
				self.image(
					"ami-new",
					"ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-20260601",
					creation_date="2026-06-01T00:00:00.000Z",
				),
			]
		)

		self.assertEqual(len(images), 1)
		self.assertEqual(AwsCatalog.image_id(images[0].provider_metadata, "Ubuntu_24.04"), "ami-new")

	def test_image_metadata_without_an_id_fails_loudly(self) -> None:
		with self.assertRaises(AwsError):
			AwsCatalog.image_id({}, "Ubuntu_24.04")

	@staticmethod
	def instance_type(
		name: str,
		*,
		bare_metal: bool = False,
		nested_virtualization: bool = False,
		architectures: list[str] | None = None,
	) -> dict:
		return {
			"InstanceType": name,
			"BareMetal": bare_metal,
			"ProcessorInfo": {
				"SupportedFeatures": ["nested-virtualization"] if nested_virtualization else [],
				"SupportedArchitectures": ["i386", "x86_64"] if architectures is None else architectures,
			},
			"VCpuInfo": {"DefaultVCpus": 128},
			"MemoryInfo": {"SizeInMiB": 262_144},
			"InstanceStorageInfo": {"TotalSizeInGB": 7_600},
			"NetworkInfo": {"MaximumNetworkInterfaces": 8},
		}

	@staticmethod
	def image(image_id: str, name: str, *, creation_date: str = "2026-01-01T00:00:00.000Z") -> dict:
		return {"ImageId": image_id, "Name": name, "CreationDate": creation_date}
