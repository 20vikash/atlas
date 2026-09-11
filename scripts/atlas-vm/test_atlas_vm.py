"""Tests for the configuration both scripts read. Run: python3 -m unittest discover scripts/atlas-vm"""

import importlib.machinery
import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

DIRECTORY = Path(__file__).resolve().parent
EXAMPLE = DIRECTORY / "atlas-vm.example.toml"


def load(name: str, filename: str):
	loader = importlib.machinery.SourceFileLoader(name, str(DIRECTORY / filename))
	spec = importlib.util.spec_from_loader(name, loader)
	module = importlib.util.module_from_spec(spec)
	sys.modules[name] = module
	loader.exec_module(module)
	return module


cli = load("atlas_vm", "atlas_vm.py")
guest = load("atlas_vm_setup", "setup.py")


class ConfigurationTest(unittest.TestCase):
	def setUp(self) -> None:
		self.directory = tempfile.TemporaryDirectory()
		self.addCleanup(self.directory.cleanup)
		self.path = Path(self.directory.name) / "atlas-vm.toml"
		self.path.write_text(EXAMPLE.read_text())

	def write(self, body: str) -> Path:
		self.path.write_text(body)
		return self.path

	def test_example_reads_in_both_scripts(self):
		settings = cli.Settings.read(self.path)
		self.assertEqual(settings.site, "atlas.example.com")
		self.assertEqual((settings.vcpu_count, settings.memory_mib, settings.disk_gib), (4, 8192, 24))
		self.assertTrue(settings.setup_script_url.endswith("/scripts/atlas-vm/setup.py"))

		configuration = guest.Configuration.read(self.path)
		self.assertEqual(configuration.admin_domain, "admin.example.com")
		self.assertEqual(configuration.bench_user, "frappe")
		self.assertEqual(configuration.images, [("24.04", "amd64", False), ("24.04", "amd64", True)])

	def test_site_and_password_are_required(self):
		path = self.write('[pilot]\nsite = "s.example.com"\n')
		for read in (cli.Settings.read, guest.Configuration.read):
			with self.assertRaises((cli.AtlasVmError, guest.SetupError)):
				read(path)

	def test_pinned_values_ignore_the_configuration(self):
		path = self.write(
			'[vm]\npython_version = "3.9"\nsetup_script_url = "https://evil.example.com/x.py"\n'
			'[pilot]\nsite = "s.example.com"\npassword = "p"\n'
		)
		self.assertIn("frappe/atlas", cli.Settings.read(path).setup_script_url)
		self.assertEqual(guest.PYTHON_VERSION, "3.14")
		self.assertIn("frappe/pilot", guest.PILOT_INSTALL_URL)

	def test_image_variants_are_checked(self):
		path = self.write(
			'[pilot]\nsite = "s.example.com"\npassword = "p"\n[[image]]\nversion = "22.04"\nminimal = true\n'
		)
		with self.assertRaises(cli.AtlasVmError):
			cli.Settings.read(path)

	def test_default_image_is_one_server(self):
		path = self.write('[pilot]\nsite = "s.example.com"\npassword = "p"\n')
		self.assertEqual(guest.Configuration.read(path).images, [("24.04", "amd64", False)])

	def test_resize_rewrites_sizes_and_keeps_the_rest(self):
		settings = cli.Settings.read(self.path)
		settings.update_sizes({"vcpu_count": 8, "disk_gib": 60})

		again = cli.Settings.read(self.path)
		self.assertEqual((again.vcpu_count, again.memory_mib, again.disk_gib), (8, 8192, 60))
		self.assertIn("# Copy to atlas-vm.toml", self.path.read_text())
		self.assertEqual(guest.Configuration.read(self.path).password, "Atlas#Bench2026x")

	def test_password_rewrite_survives_a_reread(self):
		settings = cli.Settings.read(self.path)
		password = 'quote " and \\ and $(id) #hash'
		settings.update_password(password)
		self.assertEqual(guest.Configuration.read(self.path).password, password)


class PasswordTest(unittest.TestCase):
	def test_every_class_is_present(self):
		for _ in range(50):
			password = cli.generate_password()
			self.assertEqual(len(password), 24)
			self.assertTrue(any(character.islower() for character in password))
			self.assertTrue(any(character.isupper() for character in password))
			self.assertTrue(any(character.isdigit() for character in password))
			self.assertTrue(any(character in "!@#$%^&*-_=+" for character in password))

	def test_passwords_differ(self):
		self.assertEqual(len({cli.generate_password() for _ in range(50)}), 50)


if __name__ == "__main__":
	unittest.main()
