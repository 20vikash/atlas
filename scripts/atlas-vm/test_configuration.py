"""Tests for the atlas-vm configuration readers."""

from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

DIRECTORY = Path(__file__).resolve().parent
EXAMPLE = DIRECTORY / "atlas-vm.example.toml"


def load_module(name: str, filename: str):
	spec = importlib.util.spec_from_file_location(name, DIRECTORY / filename)
	assert spec and spec.loader
	module = importlib.util.module_from_spec(spec)
	sys.modules[name] = module
	spec.loader.exec_module(module)
	return module


atlas_vm = load_module("tested_atlas_vm", "atlas_vm.py")
setup = load_module("tested_atlas_vm_setup", "setup.py")


class ConfigurationTest(unittest.TestCase):
	def setUp(self) -> None:
		self.temporary_directory = tempfile.TemporaryDirectory()
		self.addCleanup(self.temporary_directory.cleanup)
		self.path = Path(self.temporary_directory.name) / "atlas-vm.toml"
		self.path.write_text(EXAMPLE.read_text())

	def test_example_is_valid_for_both_readers(self) -> None:
		host = atlas_vm.Settings.read(self.path)
		guest = setup.Configuration.read(self.path)

		self.assertEqual(host.site, "atlas.example.com")
		self.assertEqual(guest.atlas_base_url, "https://atlas.example.com")
		self.assertEqual(guest.atlas_setup_values["region_id"], 1)
		self.assertEqual(guest.atlas_setup_values["scaleway_zone"], "fr-par-1")

	def test_atlas_settings_are_required(self) -> None:
		self.path.write_text(
			'[pilot]\nsite = "atlas.example.com"\npassword = "secret"\nletsencrypt_email = "ops@example.com"\n'
		)

		for reader in (atlas_vm.Settings.read, setup.Configuration.read):
			with self.subTest(reader=reader), self.assertRaises((atlas_vm.AtlasVmError, setup.SetupError)):
				reader(self.path)

	def test_unknown_atlas_key_is_rejected(self) -> None:
		self.path.write_text(self.path.read_text().replace('region_name = "par-1"', 'region_nmae = "par-1"'))

		for reader in (atlas_vm.Settings.read, setup.Configuration.read):
			with self.subTest(reader=reader), self.assertRaisesRegex(Exception, "atlas.region_nmae"):
				reader(self.path)

	def test_region_id_must_fit_in_the_mesh_address(self) -> None:
		self.path.write_text(self.path.read_text().replace("region_id = 1", "region_id = 65536"))

		with self.assertRaisesRegex(atlas_vm.AtlasVmError, "0 through 65535"):
			atlas_vm.Settings.read(self.path)

	def test_wildcard_domain_does_not_include_a_star(self) -> None:
		self.path.write_text(
			self.path.read_text().replace(
				'wildcard_domain = "par-1.example.com"', 'wildcard_domain = "*.par-1.example.com"'
			)
		)

		with self.assertRaisesRegex(atlas_vm.AtlasVmError, "without"):
			atlas_vm.Settings.read(self.path)

	def test_private_network_mtu_must_be_an_integer(self) -> None:
		self.path.write_text(
			self.path.read_text().replace("private_network_mtu = 1500", 'private_network_mtu = "1500"')
		)

		for reader in (atlas_vm.Settings.read, setup.Configuration.read):
			with self.subTest(reader=reader), self.assertRaisesRegex(Exception, "integer"):
				reader(self.path)

	def test_image_values_are_not_coerced(self) -> None:
		self.path.write_text(self.path.read_text().replace('version = "24.04"', "version = 24.04", 1))

		for reader in (atlas_vm.Settings.read, setup.Configuration.read):
			with (
				self.subTest(reader=reader),
				self.assertRaisesRegex(Exception, "image.version must be a string"),
			):
				reader(self.path)


if __name__ == "__main__":
	unittest.main()
