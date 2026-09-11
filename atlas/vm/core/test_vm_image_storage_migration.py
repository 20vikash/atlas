from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.object_storage import ObjectStorageError
from atlas.vm.core.vm_image_storage_migration import VirtualMachineImageStorageMigration


def build_image(**overrides) -> SimpleNamespace:
	"""Return one bootstrap System image that site files hold."""
	values = {
		"name": "image-1",
		"image_type": "System",
		"status": "Available",
		"artifact_storage": "Site File",
		"is_stored_in_site_file": True,
		"image_file": "file-rootfs",
		"kernel_file": "file-kernel",
		"image_object_key": None,
		"kernel_object_key": None,
		"image_sha256": "a" * 64,
		"kernel_sha256": "b" * 64,
		"save": Mock(),
	}
	values.update(overrides)
	return SimpleNamespace(**values)


class TestVirtualMachineImageStorageMigrationRequest(UnitTestCase):
	def test_an_object_storage_image_cannot_be_migrated(self) -> None:
		image = build_image(artifact_storage="Object Storage", is_stored_in_site_file=False)

		with self.assertRaises(frappe.ValidationError):
			VirtualMachineImageStorageMigration().request(image)

	def test_an_incomplete_image_cannot_be_migrated(self) -> None:
		with self.assertRaises(frappe.ValidationError):
			VirtualMachineImageStorageMigration().request(build_image(status="Pending"))

	def test_a_request_queues_the_migration(self) -> None:
		migration = VirtualMachineImageStorageMigration()

		with patch.object(migration, "enqueue") as enqueue:
			migration.request(build_image())

		enqueue.assert_called_once_with("image-1")


class TestVirtualMachineImageStorageMigration(UnitTestCase):
	def test_migration_uploads_moves_the_record_and_then_drops_the_files(self) -> None:
		image = build_image()
		client = Mock()

		with (
			patch(
				"atlas.vm.core.vm_image_storage_migration.frappe.get_doc",
				side_effect=[
					image,
					SimpleNamespace(get_full_path=lambda: "/site/files/aaaaaaaaaaaa-rootfs.ext4"),
					SimpleNamespace(get_full_path=lambda: "/site/files/bbbbbbbbbbbb-kernel"),
				],
			),
			patch(
				"atlas.vm.core.vm_image_storage_migration.frappe.get_single",
				return_value=SimpleNamespace(get_object_storage_client=lambda: client),
			),
			patch.object(Path, "stat", return_value=SimpleNamespace(st_size=1024)),
			patch.object(VirtualMachineImageStorageMigration, "validate_stored_size") as validate_stored_size,
			patch("atlas.vm.core.vm_image_storage_migration.frappe.db", Mock()),
			patch("atlas.vm.core.vm_image_storage_migration.frappe.delete_doc") as delete_doc,
		):
			VirtualMachineImageStorageMigration().migrate("image-1")

		self.assertEqual(image.image_object_key, f"vm-images/sha256/{'a' * 64}/rootfs.ext4")
		self.assertEqual(image.kernel_object_key, f"vm-images/sha256/{'b' * 64}/kernel")
		self.assertEqual(image.artifact_storage, "Object Storage")
		self.assertIsNone(image.image_file)
		self.assertIsNone(image.kernel_file)
		image.save.assert_called_once()
		self.assertEqual(validate_stored_size.call_count, 2)
		self.assertEqual([call.args[1] for call in delete_doc.call_args_list], ["file-rootfs", "file-kernel"])

	def test_a_migrated_image_is_left_alone(self) -> None:
		image = build_image(artifact_storage="Object Storage", is_stored_in_site_file=False)

		with (
			patch("atlas.vm.core.vm_image_storage_migration.frappe.get_doc", return_value=image),
			patch("atlas.vm.core.vm_image_storage_migration.frappe.get_single") as get_single,
		):
			VirtualMachineImageStorageMigration().migrate("image-1")

		get_single.assert_not_called()
		image.save.assert_not_called()

	def test_a_short_stored_object_fails_the_migration(self) -> None:
		client = Mock(head_object=Mock(return_value={"ContentLength": 10}))

		with self.assertRaises(ObjectStorageError):
			VirtualMachineImageStorageMigration.validate_stored_size(client, "key", 20)

	def test_a_missing_stored_object_fails_the_migration(self) -> None:
		client = Mock(head_object=Mock(return_value=None))

		with self.assertRaises(ObjectStorageError):
			VirtualMachineImageStorageMigration.validate_stored_size(client, "key", 20)
