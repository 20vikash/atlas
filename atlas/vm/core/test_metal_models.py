from __future__ import annotations

import json
import pathlib

from frappe.tests import UnitTestCase

from atlas.vm.core.metal_models import MetalVirtualMachine

SWAGGER_PATH = pathlib.Path(__file__).parents[3] / "metal" / "internal" / "api" / "swagger.json"


def complete_response() -> dict:
	return {
		"id": "VM-00001",
		"desired": {
			"generation": 3,
			"restart_generation": 1,
			"state": "running",
			"compute": {
				"virtual_cpu_count": 2,
				"memory_mib": 2048,
				"is_sleepy": True,
				"idle_timeout_seconds": 1800,
			},
			"disk": {"size_mib": 8192, "throughput_mibps": 100, "iops": 500},
			"image": {
				"ref": "ubuntu",
				"architecture": "amd64",
				"rootfs": {"sha256": "a" * 64},
				"kernel": {"sha256": "b" * 64},
				"cache_image": True,
				"memory_snapshot": True,
				"memory_snapshot_configuration": {
					"virtual_cpu_count": 2,
					"memory_mib": 2048,
					"disk_mib": 8192,
				},
			},
			"network": {
				"egress": "uplink",
				"public_ipv4": "1.2.3.4",
				"wireguard_mesh_ipv6": "fdaa::1",
				"private_network_throughput_mibps": 10,
				"public_network_throughput_mibps": 20,
			},
			"guest": {"hostname": "web", "ssh_keys": ["ssh-ed25519 AAAA"], "metadata": {"role": "web"}},
		},
		"observed": {
			"generation": 3,
			"restart_generation": 1,
			"state": "running",
			"updated_at": "2026-09-06T10:00:00Z",
			"disk": {"used_mib": 1024},
			"network": {"mac": "06:00:ac:10:00:02"},
			"error": {
				"code": "operation_failed",
				"message": "network failed",
				"updated_at": "2026-09-06T10:00:00Z",
			},
		},
	}


def new_virtual_machine_response() -> dict:
	"""Return what Metal sends for a VM it has accepted but not yet reconciled."""
	return {
		"id": "VM-00002",
		"desired": {
			"generation": 1,
			"restart_generation": 0,
			"state": "running",
			"compute": {
				"virtual_cpu_count": 0,
				"memory_mib": 0,
				"is_sleepy": False,
				"idle_timeout_seconds": 0,
			},
			"disk": {"size_mib": 0, "throughput_mibps": 0, "iops": 0},
			"image": {
				"ref": "ubuntu",
				"architecture": "amd64",
				"rootfs": {"sha256": "a" * 64},
				"kernel": {"sha256": "b" * 64},
				"cache_image": False,
				"memory_snapshot": False,
			},
			"network": {
				"egress": "uplink",
				"wireguard_mesh_ipv6": "",
				"private_network_throughput_mibps": 0,
				"public_network_throughput_mibps": 0,
			},
			"guest": {"hostname": "", "ssh_keys": [], "metadata": {}},
		},
		"observed": {
			"generation": 0,
			"restart_generation": 0,
			"state": "unknown",
			"updated_at": "2026-09-06T10:00:00Z",
			"disk": {"used_mib": 0},
			"network": {},
		},
	}


class TestMetalVirtualMachineParsing(UnitTestCase):
	def test_a_complete_response_parses_into_typed_values(self) -> None:
		machine = MetalVirtualMachine.from_dict(complete_response())

		self.assertEqual(machine.id, "VM-00001")
		self.assertEqual(machine.desired.compute.virtual_cpu_count, 2)
		self.assertTrue(machine.desired.compute.is_sleepy)
		self.assertEqual(machine.desired.compute.idle_timeout_seconds, 1800)
		self.assertEqual(machine.desired.image.rootfs.sha256, "a" * 64)
		self.assertEqual(machine.desired.guest.ssh_keys, ("ssh-ed25519 AAAA",))
		self.assertEqual(machine.desired.guest.metadata, {"role": "web"})
		self.assertEqual(machine.observed.network.mac, "06:00:ac:10:00:02")
		self.assertEqual(machine.observed.error.message, "network failed")

	def test_a_new_virtual_machine_parses_with_empty_values(self) -> None:
		"""Metal sends every group for a VM it has not reconciled yet."""
		machine = MetalVirtualMachine.from_dict(new_virtual_machine_response())

		self.assertEqual(machine.desired.state, "running")
		self.assertEqual(machine.desired.compute.virtual_cpu_count, 0)
		self.assertEqual(machine.desired.guest.ssh_keys, ())
		self.assertEqual(machine.observed.state, "unknown")
		self.assertIsNone(machine.observed.error)

	def test_an_omitted_public_ipv4_is_tolerated(self) -> None:
		"""Metal omits public_ipv4 when no address is attached."""
		response = new_virtual_machine_response()
		self.assertNotIn("public_ipv4", response["desired"]["network"])

		machine = MetalVirtualMachine.from_dict(response)

		self.assertEqual(machine.desired.network.public_ipv4, "")

	def test_a_response_missing_a_required_group_is_rejected(self) -> None:
		"""A truncated response must fail loudly, not read as an empty VM."""
		for removed in ("desired", "observed"):
			response = new_virtual_machine_response()
			del response[removed]
			with self.assertRaises(ValueError):
				MetalVirtualMachine.from_dict(response)

	def test_an_absent_memory_snapshot_configuration_stays_none(self) -> None:
		response = complete_response()
		del response["desired"]["image"]["memory_snapshot_configuration"]

		machine = MetalVirtualMachine.from_dict(response)

		self.assertIsNone(machine.desired.image.memory_snapshot_configuration)

	def test_as_dict_is_json_compatible(self) -> None:
		"""as_dict feeds a JSON response, so it must survive an encode and decode."""
		machine = MetalVirtualMachine.from_dict(complete_response())

		decoded = json.loads(json.dumps(machine.as_dict()))

		self.assertEqual(MetalVirtualMachine.from_dict(decoded), machine)


class TestMetalContract(UnitTestCase):
	"""Fail when Metal's published response shape stops matching what Atlas reads."""

	def load_definitions(self) -> dict:
		if not SWAGGER_PATH.exists():
			self.skipTest(f"Metal OpenAPI document not found at {SWAGGER_PATH}")
		return json.loads(SWAGGER_PATH.read_text())["definitions"]

	def assert_has_fields(self, definitions: dict, name: str, fields: set[str]) -> None:
		published = set(definitions[name]["properties"])
		self.assertTrue(
			fields <= published,
			f"{name} no longer publishes {sorted(fields - published)}",
		)

	def test_metal_publishes_every_field_atlas_reads(self) -> None:
		definitions = self.load_definitions()

		self.assert_has_fields(
			definitions,
			"api.desiredVirtualMachineResponse",
			{"generation", "restart_generation", "state", "compute", "disk", "image", "network", "guest"},
		)
		self.assert_has_fields(
			definitions,
			"api.computeResponse",
			{"virtual_cpu_count", "memory_mib", "is_sleepy", "idle_timeout_seconds"},
		)
		self.assert_has_fields(
			definitions,
			"api.observedVirtualMachineResponse",
			{"generation", "restart_generation", "state", "updated_at", "disk", "network", "error"},
		)
		self.assert_has_fields(definitions, "api.observedDiskResponse", {"used_mib"})
		self.assert_has_fields(definitions, "api.operationErrorResponse", {"code", "message", "updated_at"})
		self.assert_has_fields(definitions, "api.virtualMachineResponse", {"id", "desired", "observed"})
		self.assert_has_fields(
			definitions,
			"api.virtualMachineImageResponse",
			{"ref", "architecture", "rootfs", "kernel", "cache_image", "memory_snapshot"},
		)

	def test_metal_still_serves_the_routes_atlas_calls(self) -> None:
		if not SWAGGER_PATH.exists():
			self.skipTest(f"Metal OpenAPI document not found at {SWAGGER_PATH}")
		paths = set(json.loads(SWAGGER_PATH.read_text())["paths"])

		expected = {
			"/v1/sync",
			"/v1/vms/{id}",
			"/v1/vms/{id}/power",
			"/v1/vms/{id}/restart",
			"/v1/vms/{id}/compute",
			"/v1/vms/{id}/disk",
			"/v1/vms/{id}/network",
			"/v1/vms/{id}/ssh-keys",
			"/v1/vms/{id}/metadata",
			"/v1/vms/{id}/snapshots",
			"/v1/snapshots/{id}",
			"/v1/snapshots/{id}/upload",
		}
		self.assertTrue(expected <= paths, f"Metal no longer serves {sorted(expected - paths)}")
