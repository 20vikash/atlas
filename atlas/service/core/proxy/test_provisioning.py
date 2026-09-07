from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import MagicMock, Mock, patch

import frappe
from frappe.tests import UnitTestCase

import atlas.service.core.proxy.provisioning as provisioning
from atlas.atlas.core.ssh import SSHResult
from atlas.service.core.proxy.provisioning import ProxyServerProvisioner
from atlas.vm.core.metal_client import MetalClientError
from atlas.vm.core.vm_service import VirtualMachineCreateError


def _proxy_server(**values) -> SimpleNamespace:
	defaults = {
		"doctype": "Proxy Server",
		"name": "proxy-001",
		"status": "Pending",
		"virtual_machine": None,
		"virtual_machine_image": "image-1",
		"server_ip_address": None,
		"public_ipv4": None,
		"get_domain": lambda: "proxy-001.example.com",
		"vcpus": 2,
		"memory_mib": 4096,
		"disk_mib": 16384,
		"tenant_id": 0,
		"installed_package_hash": None,
		"pushed_config_hash": None,
		"tls_expires_on": None,
		"is_provisioning_completed": 0,
		"failure_message": None,
		"save": Mock(),
	}
	proxy_server = SimpleNamespace(**(defaults | values))
	return proxy_server


class TestProvisioningSequence(UnitTestCase):
	def test_the_steps_install_before_they_configure(self) -> None:
		"""One setup run installs the command that applies the configuration."""
		phases = [phase for phase, _operation in ProxyServerProvisioner(_proxy_server()).steps]

		self.assertEqual(phases, ["virtual-machine", "dns", "secure-shell", "package", "configuration"])

	def test_a_failed_step_records_its_phase_and_stops(self) -> None:
		proxy_server = _proxy_server()
		provisioner = ProxyServerProvisioner(proxy_server)
		later_step = Mock()
		with (
			patch.object(
				ProxyServerProvisioner,
				"steps",
				new=property(
					lambda self: (
						("virtual-machine", Mock(side_effect=RuntimeError("no capacity"))),
						("secure-shell", later_step),
					)
				),
			),
			patch.object(provisioning.frappe.db, "commit"),
			patch.object(provisioning.frappe, "log_error"),
			patch.object(provisioner, "save_progress"),
			self.assertRaises(RuntimeError),
		):
			provisioner.run()

		self.assertEqual(proxy_server.status, "Failed")
		self.assertIn("virtual-machine", proxy_server.failure_message)
		later_step.assert_not_called()


class TestCreateVirtualMachine(UnitTestCase):
	def build(self, create_result: dict, **values):
		proxy_server = _proxy_server(**values)
		provisioner = ProxyServerProvisioner(proxy_server)
		self.service = MagicMock()
		self.service.create.return_value = create_result
		patches = (
			patch.object(provisioner, "save_progress"),
			patch.object(
				provisioning.frappe,
				"get_single",
				return_value=SimpleNamespace(public_ssh_key="ssh-ed25519 AAAA atlas"),
			),
			patch("atlas.vm.core.vm_service.VirtualMachineService", self.service),
			patch(
				"atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.reserve",
				return_value="203.0.113.9",
			),
		)
		for patched in patches:
			patched.start()
			self.addCleanup(patched.stop)
		return proxy_server, provisioner

	def test_the_machine_carries_the_atlas_key_and_a_public_address(self) -> None:
		proxy_server, provisioner = self.build({"name": "vm-00001", "is_draft": False})

		provisioner.create_virtual_machine()

		request = self.service.create.call_args.args[0]
		self.assertEqual(request["ssh_keys"], "ssh-ed25519 AAAA atlas")
		self.assertEqual(request["egress"], "uplink")
		self.assertEqual(request["server_ip_address"], "203.0.113.9")
		self.assertEqual(request["hostname"], "proxy-001")
		self.assertEqual(proxy_server.virtual_machine, "vm-00001")

	# Metal never confirmed the create, so the VM reconciler settles it first.
	def test_an_uncertain_create_records_the_name_and_stops(self) -> None:
		proxy_server, provisioner = self.build({"name": "vm-00002", "is_draft": True})

		with self.assertRaises(frappe.ValidationError):
			provisioner.create_virtual_machine()

		self.assertEqual(proxy_server.virtual_machine, "vm-00002")

	def test_a_confirmed_create_failure_records_the_draft_name(self) -> None:
		proxy_server, provisioner = self.build({})
		self.service.create.side_effect = VirtualMachineCreateError("vm-00002", MetalClientError("rejected"))

		with self.assertRaises(VirtualMachineCreateError):
			provisioner.create_virtual_machine()

		self.assertEqual(proxy_server.virtual_machine, "vm-00002")

	def test_an_existing_machine_is_not_built_again(self) -> None:
		_proxy, provisioner = self.build({}, virtual_machine="vm-00003")
		with (
			patch.object(provisioning.frappe.db, "exists", return_value=True),
			patch.object(provisioning.frappe, "get_doc", return_value=SimpleNamespace(is_draft=False)),
		):
			provisioner.create_virtual_machine()

		self.service.create.assert_not_called()

	def test_a_missing_reconciled_machine_is_created_again(self) -> None:
		proxy_server, provisioner = self.build(
			{"name": "vm-00004", "is_draft": False},
			virtual_machine="vm-00003",
			server_ip_address="203.0.113.9",
		)
		with patch.object(provisioning.frappe.db, "exists", return_value=False):
			provisioner.create_virtual_machine()

		self.assertEqual(proxy_server.virtual_machine, "vm-00004")
		self.assertEqual(self.service.create.call_args.args[0]["server_ip_address"], "203.0.113.9")


class TestApplySteps(UnitTestCase):
	def test_an_unchanged_configuration_is_not_sent_again(self) -> None:
		proxy_server = _proxy_server(pushed_config_hash="digest-1")
		provisioner = ProxyServerProvisioner(proxy_server)
		with (
			patch.object(provisioning, "ProxyConfiguration") as configuration,
			patch.object(provisioning, "SSHRunner") as ssh_runner,
		):
			configuration.return_value.digest = "digest-1"

			provisioner.push_configuration()

		ssh_runner.assert_not_called()

	def test_a_rejected_configuration_fails_loudly(self) -> None:
		proxy_server = _proxy_server()
		provisioner = ProxyServerProvisioner(proxy_server)
		with (
			patch.object(provisioning, "ProxyConfiguration") as configuration,
			patch.object(provisioning, "SSHRunner") as ssh_runner,
			patch.object(provisioner.__class__, "ssh_host", new=property(lambda self: "203.0.113.9")),
		):
			configuration.return_value.digest = "digest-2"
			ssh_runner.return_value.run_command.return_value = SSHResult("bad certificate", 1)

			with self.assertRaises(frappe.ValidationError):
				provisioner.push_configuration()

		self.assertIsNone(proxy_server.pushed_config_hash)

	def test_an_installed_package_is_not_installed_again(self) -> None:
		proxy_server = _proxy_server(installed_package_hash="sha-1", virtual_machine="vm-00001")
		provisioner = ProxyServerProvisioner(proxy_server)
		with (
			patch.object(
				provisioning.frappe,
				"get_single",
				return_value={"http_proxy_package_file": "file-1", "http_proxy_package_hash": "sha-1"},
			),
			patch.object(provisioning.SSHTask, "create_for_script_file") as create_task,
		):
			provisioner.install_package()

		create_task.assert_not_called()

	def test_a_missing_package_fails_loudly(self) -> None:
		provisioner = ProxyServerProvisioner(_proxy_server())
		with patch.object(provisioning.frappe, "get_single", return_value={}):
			with self.assertRaises(frappe.ValidationError):
				provisioner.install_package()


class TestDNSRecord(UnitTestCase):
	def build(self, **values):
		proxy_server = _proxy_server(**values)
		provisioner = ProxyServerProvisioner(proxy_server)
		self.provider = MagicMock()
		patches = (
			patch.object(
				provisioning.frappe,
				"get_single",
				return_value=SimpleNamespace(dns_provider_controller=self.provider),
			),
			patch.object(provisioning.frappe, "get_doc", return_value=proxy_server),
		)
		for patched in patches:
			patched.start()
			self.addCleanup(patched.stop)
		return proxy_server, provisioner

	def test_the_name_points_at_the_reserved_address(self) -> None:
		_, provisioner = self.build(public_ipv4="203.0.113.9")

		provisioner.update_dns_record()

		self.provider.upsert_a_record.assert_called_once_with("proxy-001.example.com", "203.0.113.9")

	def test_a_missing_address_fails_loudly(self) -> None:
		_proxy, provisioner = self.build(public_ipv4=None)

		with self.assertRaises(frappe.ValidationError):
			provisioner.update_dns_record()

		self.provider.upsert_a_record.assert_not_called()

	def test_an_archived_proxy_does_not_restore_its_record(self) -> None:
		proxy_server, provisioner = self.build(status="Archived", public_ipv4="203.0.113.9")

		provisioner.update_dns_record()

		self.assertIs(provisioner.proxy_server, proxy_server)
		self.provider.upsert_a_record.assert_not_called()
