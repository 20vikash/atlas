"""Tests for the atlas-vm configuration readers."""

from __future__ import annotations

import importlib.util
import json
import string
import sys
import tempfile
import unittest
from pathlib import Path
from types import ModuleType
from unittest.mock import PropertyMock, patch

DIRECTORY = Path(__file__).resolve().parent
EXAMPLE = DIRECTORY / "atlas-vm.example.toml"


def load_module(name: str, filename: str) -> ModuleType:
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
		with patch.object(setup, "generate_password", return_value="generated-bootstrap-password"):
			guest = setup.Configuration.read(self.path)

		self.assertEqual(host.site, "atlas.example.com")
		self.assertEqual(guest.atlas_base_url, "https://atlas.example.com")
		self.assertEqual(guest.atlas_setup_values["region_id"], 1)
		self.assertEqual(guest.atlas_setup_values["scaleway_zone"], "fr-par-1")
		self.assertEqual(guest.bootstrap_password, "generated-bootstrap-password")

	def test_atlas_settings_are_required(self) -> None:
		self.path.write_text('[pilot]\nsite = "atlas.example.com"\nletsencrypt_email = "ops@example.com"\n')

		with self.assertRaises(atlas_vm.AtlasVmError):
			atlas_vm.Settings.read(self.path)

	def test_unknown_atlas_key_is_rejected(self) -> None:
		self.path.write_text(self.path.read_text().replace('region_name = "par-1"', 'region_nmae = "par-1"'))

		with self.assertRaisesRegex(atlas_vm.AtlasVmError, "atlas.region_nmae"):
			atlas_vm.Settings.read(self.path)

	def test_pilot_password_is_rejected(self) -> None:
		self.path.write_text(
			self.path.read_text().replace("[pilot]\n", '[pilot]\npassword = "do-not-store-this"\n')
		)

		with self.assertRaisesRegex(atlas_vm.AtlasVmError, "pilot.password"):
			atlas_vm.Settings.read(self.path)

	def test_guest_generates_a_strong_bootstrap_password(self) -> None:
		password = setup.generate_password()

		self.assertEqual(len(password), 24)
		for characters in (string.ascii_lowercase, string.ascii_uppercase, string.digits, "!@#$%^&*-_=+"):
			self.assertTrue(set(password) & set(characters))

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

		with self.assertRaisesRegex(atlas_vm.AtlasVmError, "integer"):
			atlas_vm.Settings.read(self.path)

	def test_image_values_are_not_coerced(self) -> None:
		self.path.write_text(self.path.read_text().replace('version = "24.04"', "version = 24.04", 1))

		with self.assertRaisesRegex(atlas_vm.AtlasVmError, "image.version must be a string"):
			atlas_vm.Settings.read(self.path)

	def test_guest_setup_reuses_the_ssh_key_and_forwards_json(self) -> None:
		configuration = setup.Configuration.read(self.path)
		private_key = Path(self.temporary_directory.name) / "id_ed25519"
		private_key.touch()
		stage = setup.Setup(configuration)

		with (
			patch.object(
				setup.Configuration,
				"fleet_private_key_path",
				new_callable=PropertyMock,
				return_value=private_key,
			),
			patch.object(stage, "as_bench") as as_bench,
			patch.object(stage, "bench_output", return_value="ssh-ed25519 public-key"),
			patch.object(stage, "pilot") as pilot,
		):
			stage.configure_atlas()

		as_bench.assert_not_called()
		self.assertEqual(pilot.call_count, 2)
		forwarded = json.loads(pilot.call_args_list[1].kwargs["input_text"])
		self.assertEqual(forwarded["public_ssh_key"], "ssh-ed25519 public-key")
		self.assertNotIn("bootstrap_password", forwarded)


if __name__ == "__main__":
	unittest.main()
