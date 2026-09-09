from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core.metal_client import MetalClientError
from atlas.vm.core.placement import PlacementService
from atlas.vm.core.vm_service import VirtualMachineCreateError, VirtualMachineService
from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage


def build_image(tenant_id: int, image_type: str = "Machine") -> VirtualMachineImage:
	"""Return one image document that answers the tenant visibility rule."""
	image = VirtualMachineImage.__new__(VirtualMachineImage)
	image.tenant_id = tenant_id
	image.image_type = image_type
	image.title = "Worker snapshot"
	image.enabled = 1
	image.validate_is_available = Mock()
	return image


class TestVirtualMachineCreation(UnitTestCase):
	def test_creation_commits_the_draft_before_the_metal_request(self) -> None:
		operations: list[str] = []
		image = SimpleNamespace(
			name="image-1",
			platform="amd64",
			enabled=1,
			title="Ubuntu",
			validate_compatibility=Mock(),
		)
		server = SimpleNamespace(name="server-1")
		virtual_machine = SimpleNamespace(
			name="VM-00001",
			flags=SimpleNamespace(),
			is_draft=1,
			save=Mock(side_effect=lambda **arguments: operations.append("save")),
		)
		metal_client = Mock()
		metal_client.put_virtual_machine.side_effect = lambda *arguments: operations.append("metal")

		with (
			patch.object(VirtualMachineService, "get_image", return_value=image),
			patch.object(PlacementService, "select_server", return_value=server),
			patch.object(VirtualMachineService, "insert_draft", return_value=virtual_machine),
			patch.object(VirtualMachineService, "get_metal_request", return_value={"request": True}),
			patch(
				"atlas.vm.core.vm_service.frappe.db.commit",
				side_effect=lambda: operations.append("commit"),
			),
			patch("atlas.vm.core.vm_service.MetalClient", return_value=metal_client),
		):
			result = VirtualMachineService.create(self.request())

		self.assertEqual(result, {"name": "VM-00001", "is_draft": False})
		self.assertEqual(operations, ["commit", "metal", "save"])
		self.assertEqual(virtual_machine.is_draft, 0)

	def test_a_system_image_can_boot_for_any_tenant(self) -> None:
		image = build_image(tenant_id=0, image_type="System")

		with patch("atlas.vm.core.vm_service.frappe.get_doc", return_value=image):
			self.assertIs(VirtualMachineService.get_image("system-image", 7), image)

		image.validate_is_available.assert_called_once()

	def test_another_tenant_machine_image_cannot_boot(self) -> None:
		image = build_image(tenant_id=8)

		with (
			patch("atlas.vm.core.vm_service.frappe.get_doc", return_value=image),
			self.assertRaises(frappe.DoesNotExistError),
		):
			VirtualMachineService.get_image("machine-image", 7)

	def test_uncertain_create_keeps_the_committed_draft(self) -> None:
		image = SimpleNamespace(
			name="image-1",
			platform="amd64",
			enabled=1,
			title="Ubuntu",
			validate_compatibility=Mock(),
		)
		server = SimpleNamespace(name="server-1")
		virtual_machine = SimpleNamespace(
			name="VM-00001",
			flags=SimpleNamespace(),
			is_draft=1,
			save=Mock(),
		)
		metal_client = Mock()
		metal_client.put_virtual_machine.side_effect = MetalClientError("lost", uncertain=True)

		with (
			patch.object(VirtualMachineService, "get_image", return_value=image),
			patch.object(PlacementService, "select_server", return_value=server),
			patch.object(VirtualMachineService, "insert_draft", return_value=virtual_machine),
			patch.object(VirtualMachineService, "get_metal_request", return_value={"request": True}),
			patch("atlas.vm.core.vm_service.frappe.db.commit") as commit,
			patch("atlas.vm.core.vm_service.MetalClient", return_value=metal_client),
		):
			result = VirtualMachineService.create(self.request())

		self.assertEqual(result, {"name": "VM-00001", "is_draft": True})
		commit.assert_called_once()
		virtual_machine.save.assert_not_called()

	def test_confirmed_create_failure_identifies_the_committed_draft(self) -> None:
		image = SimpleNamespace(
			name="image-1",
			platform="amd64",
			enabled=1,
			title="Ubuntu",
			validate_compatibility=Mock(),
		)
		server = SimpleNamespace(name="server-1")
		virtual_machine = SimpleNamespace(
			name="VM-00001",
			flags=SimpleNamespace(),
			is_draft=1,
			save=Mock(),
		)
		metal_client = Mock()
		metal_client.put_virtual_machine.side_effect = MetalClientError("rejected", status=400)

		with (
			patch.object(VirtualMachineService, "get_image", return_value=image),
			patch.object(PlacementService, "select_server", return_value=server),
			patch.object(VirtualMachineService, "insert_draft", return_value=virtual_machine),
			patch.object(VirtualMachineService, "get_metal_request", return_value={"request": True}),
			patch("atlas.vm.core.vm_service.frappe.db.commit") as commit,
			patch("atlas.vm.core.vm_service.MetalClient", return_value=metal_client),
			self.assertRaises(VirtualMachineCreateError) as raised,
		):
			VirtualMachineService.create(self.request())

		self.assertEqual(raised.exception.virtual_machine_name, "VM-00001")
		commit.assert_called_once()
		virtual_machine.save.assert_not_called()

	@staticmethod
	def request() -> dict[str, int | str]:
		return {
			"virtual_machine_image": "image-1",
			"vcpus": 2,
			"memory_mib": 2048,
			"disk_mib": 10240,
			"tenant_id": 7,
		}


class TestVirtualMachineInformation(UnitTestCase):
	def test_missing_virtual_machine_returns_no_information(self) -> None:
		virtual_machine = SimpleNamespace(name="VM-00001", server="server-1")
		metal_client = Mock()
		metal_client.get_virtual_machine.side_effect = MetalClientError("not found", status=404)

		with (
			patch("atlas.vm.core.vm_service.frappe.get_doc", return_value=Mock()),
			patch("atlas.vm.core.vm_service.MetalClient", return_value=metal_client),
		):
			information = VirtualMachineService(virtual_machine).get_information()

		self.assertIsNone(information)

	def test_metal_failure_is_visible_to_the_caller(self) -> None:
		virtual_machine = SimpleNamespace(name="VM-00001", server="server-1")
		metal_client = Mock()
		metal_client.get_virtual_machine.side_effect = MetalClientError("connection refused")

		with (
			patch("atlas.vm.core.vm_service.frappe.get_doc", return_value=Mock()),
			patch("atlas.vm.core.vm_service.MetalClient", return_value=metal_client),
			self.assertRaisesRegex(frappe.ValidationError, "connection refused"),
		):
			VirtualMachineService(virtual_machine).get_information()
