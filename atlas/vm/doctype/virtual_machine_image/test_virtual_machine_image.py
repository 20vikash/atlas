import hashlib
from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.object_storage import ObjectStorageError
from atlas.vm.core.metal_client import MetalClientError
from atlas.vm.core.multipart_upload import (
	MEBIBYTE,
	MULTIPART_PART_SIZE_MIB,
	MultipartUploadService,
	bytes_to_mib,
	get_multipart_part_count,
)
from atlas.vm.core.vm_image_transfer import VirtualMachineImageTransferService
from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage


def artifact_url(image, artifact, expiry_seconds=0) -> str:
	"""Return one predictable artifact URL in place of a signed one."""
	return f"{artifact}-url"


class TestVirtualMachineImage(UnitTestCase):
	def make_image(self, **values):
		defaults = {
			"image_sha256": "a" * 64,
			"kernel_sha256": "b" * 64,
			"artifact_storage": "Object Storage",
			"image_object_key": "images/image/rootfs.img",
			"kernel_object_key": "images/image/kernel",
			"image_file": None,
			"kernel_file": None,
			"image_size_mib": 10,
			"kernel_size_mib": 5,
			"platform": "amd64",
			"status": "Available",
			"title": "Machine image",
			"cache_image": 0,
			"memory_snapshot": 0,
			"memory_snapshot_virtual_cpu_count": 0,
			"memory_snapshot_memory_mib": 0,
			"memory_snapshot_disk_mib": 0,
		}
		defaults.update(values)
		image = object.__new__(VirtualMachineImage)
		for key, value in defaults.items():
			setattr(image, key, value)
		return image

	def test_an_archived_image_cannot_boot_a_virtual_machine(self) -> None:
		image = self.make_image(status="Archived")

		with self.assertRaises(frappe.ValidationError):
			image.validate_is_available()

	def test_download_returns_only_the_selected_artifact(self) -> None:
		image = self.make_image()
		with (
			patch.object(VirtualMachineImage, "validate_is_available"),
			patch.object(VirtualMachineImage, "get_artifact_url", artifact_url),
		):
			rootfs = image.get_presigned_download_url("rootfs")
			kernel = image.get_presigned_download_url("kernel")

		self.assertEqual(rootfs["artifact"], "rootfs")
		self.assertEqual(rootfs["url"], "rootfs-url")
		self.assertEqual(rootfs["size_mib"], 10)
		self.assertEqual(kernel["artifact"], "kernel")
		self.assertEqual(kernel["url"], "kernel-url")
		self.assertEqual(kernel["size_mib"], 5)

	def test_metal_request_contains_immutable_image_data(self) -> None:
		image = self.make_image()

		with patch.object(VirtualMachineImage, "get_artifact_url", artifact_url):
			request = image.get_metal_image_request()

		identity = f"amd64\0{'a' * 64}\0{'b' * 64}"
		expected_reference = hashlib.sha256(identity.encode()).hexdigest()
		self.assertEqual(request["ref"], f"sha256:{expected_reference}")
		self.assertEqual(request["rootfs"]["sha256"], "a" * 64)
		self.assertEqual(request["kernel"]["sha256"], "b" * 64)

	def test_a_site_file_image_uses_a_public_download_url(self) -> None:
		image = self.make_image(
			artifact_storage="Site File",
			image_object_key=None,
			kernel_object_key=None,
			image_file="file-rootfs",
			kernel_file="file-kernel",
		)

		with patch(
			"atlas.vm.doctype.virtual_machine_image.virtual_machine_image.get_download_url",
			side_effect=lambda name: f"https://atlas.test/files/{name}",
		) as get_download_url:
			self.assertEqual(image.get_artifact_url("rootfs"), "https://atlas.test/files/file-rootfs")
			self.assertEqual(image.get_artifact_url("kernel"), "https://atlas.test/files/file-kernel")

		self.assertEqual(get_download_url.call_count, 2)

	def test_an_unknown_artifact_is_refused(self) -> None:
		image = self.make_image()
		with self.assertRaisesRegex(frappe.ValidationError, "rootfs or kernel"):
			image.get_presigned_download_url("memory")

	def test_a_site_file_image_has_no_signed_download(self) -> None:
		image = self.make_image(
			artifact_storage="Site File", image_file="file-rootfs", kernel_file="file-kernel"
		)

		with self.assertRaises(frappe.ValidationError):
			image.get_presigned_download_url("rootfs")

	def test_only_a_system_image_can_use_site_file_storage(self) -> None:
		image = self.make_image(
			artifact_storage="Site File",
			image_type="machine",
			image_file="file-rootfs",
			kernel_file="file-kernel",
		)

		with self.assertRaises(frappe.ValidationError):
			image.validate_artifacts()

	def test_a_site_file_image_requires_both_files(self) -> None:
		image = self.make_image(
			artifact_storage="Site File", image_type="system", image_file="file-rootfs", kernel_file=None
		)

		with self.assertRaises(frappe.ValidationError):
			image.validate_artifacts()

	def test_image_requires_disk_to_hold_rootfs(self) -> None:
		image = self.make_image(image_size_mib=10240)

		image.validate_compatibility(disk_mib=10240)
		image.validate_compatibility(disk_mib=20480)
		with self.assertRaises(frappe.ValidationError):
			image.validate_compatibility(disk_mib=1024)

	def test_memory_snapshot_image_allows_any_matching_disk(self) -> None:
		# Metal cold boots on a spec mismatch, so a memory snapshot image only needs
		# a disk that holds its root file system.
		image = self.make_image(
			image_size_mib=10240,
			memory_snapshot=1,
			memory_snapshot_virtual_cpu_count=2,
			memory_snapshot_memory_mib=2048,
			memory_snapshot_disk_mib=10240,
		)

		image.validate_compatibility(disk_mib=20480)
		with self.assertRaises(frappe.ValidationError):
			image.validate_compatibility(disk_mib=1024)

	def test_memory_snapshot_requires_positive_configuration(self) -> None:
		image = self.make_image(
			memory_snapshot=1,
			memory_snapshot_virtual_cpu_count=2,
			memory_snapshot_memory_mib=0,
			memory_snapshot_disk_mib=1024,
		)

		with self.assertRaises(frappe.ValidationError):
			image.validate_memory_snapshot_configuration()

	def test_memory_without_cache_remains_a_normal_image_request(self) -> None:
		image = self.make_image(
			memory_snapshot=1,
			memory_snapshot_virtual_cpu_count=2,
			memory_snapshot_memory_mib=2048,
			memory_snapshot_disk_mib=10240,
		)
		with (
			patch.object(VirtualMachineImage, "get_artifact_url", artifact_url),
		):
			request = image.get_metal_image_request()

		self.assertNotIn("cache_image", request)
		self.assertNotIn("memory_snapshot", request)

	def test_cached_vm_request_contains_memory_policy(self) -> None:
		image = self.make_image(
			cache_image=1,
			memory_snapshot=1,
			memory_snapshot_virtual_cpu_count=2,
			memory_snapshot_memory_mib=2048,
			memory_snapshot_disk_mib=10240,
		)
		with (
			patch.object(VirtualMachineImage, "get_artifact_url", artifact_url),
		):
			request = image.get_metal_image_request()

		self.assertTrue(request["cache_image"])
		self.assertTrue(request["memory_snapshot"])

	def test_desired_image_contains_memory_policy(self) -> None:
		image = self.make_image(
			cache_image=1,
			memory_snapshot=1,
			memory_snapshot_virtual_cpu_count=2,
			memory_snapshot_memory_mib=2048,
			memory_snapshot_disk_mib=10240,
		)
		with (
			patch.object(VirtualMachineImage, "get_artifact_url", artifact_url),
		):
			request = image.get_desired_image()

		self.assertTrue(request["cache_image"])
		self.assertTrue(request["memory_snapshot"])
		self.assertEqual(
			request["memory_snapshot_configuration"],
			{"virtual_cpu_count": 2, "memory_mib": 2048, "disk_mib": 10240},
		)


class TestVirtualMachineImageTransfer(UnitTestCase):
	def test_snapshot_uuid_becomes_machine_image_name(self) -> None:
		virtual_machine = SimpleNamespace(
			name="VM-00001",
			server="server-1",
			virtual_machine_image="system-image",
			disk_mib=1024,
			tenant_id=7,
		)
		original_image = SimpleNamespace(
			platform="amd64",
			operating_system="Ubuntu",
			operating_system_version="24.04",
		)
		server = SimpleNamespace(name="server-1")
		image = Mock()
		metal_client = Mock()
		metal_client.create_snapshot.return_value = {
			"id": "01900000-0000-7000-8000-000000000001",
			"rootfs": {"size_bytes": 1024 * 1024},
			"kernel": {"size_bytes": 1024 * 1024},
		}
		service = VirtualMachineImageTransferService()

		def get_doc(doctype, name=None):
			if isinstance(doctype, dict):
				return image
			if doctype == "Virtual Machine Image":
				return original_image
			return server

		with (
			patch("atlas.vm.core.vm_image_transfer.frappe.get_doc", side_effect=get_doc),
			patch("atlas.vm.core.vm_image_transfer.MetalClient", return_value=metal_client),
			patch.object(service, "enqueue") as enqueue_transfer,
		):
			image_name = service.create_from_virtual_machine(virtual_machine, "Machine image")

		self.assertEqual(image_name, "01900000-0000-7000-8000-000000000001")
		image.insert.assert_called_once_with(
			set_name="01900000-0000-7000-8000-000000000001",
		)
		enqueue_transfer.assert_called_once_with("01900000-0000-7000-8000-000000000001")

	def test_part_count_has_no_empty_boundary_part(self) -> None:
		self.assertEqual(get_multipart_part_count(MULTIPART_PART_SIZE_MIB), 1)
		self.assertEqual(get_multipart_part_count(MULTIPART_PART_SIZE_MIB + 1), 2)

	def test_bytes_round_up_to_whole_mib(self) -> None:
		self.assertEqual(bytes_to_mib(1), 1)
		self.assertEqual(bytes_to_mib(MEBIBYTE), 1)
		self.assertEqual(bytes_to_mib(MEBIBYTE + 1), 2)

	def test_upload_request_signs_each_part_for_one_day(self) -> None:
		image = SimpleNamespace(
			image_object_key="images/image/rootfs.img",
			kernel_object_key="images/image/kernel",
			rootfs_multipart_upload_id="rootfs-upload",
			kernel_multipart_upload_id="kernel-upload",
			image_size_mib=MULTIPART_PART_SIZE_MIB + 1,
			kernel_size_mib=1,
		)
		object_storage_client = Mock()
		object_storage_client.sign_upload_part.side_effect = lambda key, upload_id, part, **kwargs: (
			f"url-{part}"
		)

		request = MultipartUploadService(image, object_storage_client).get_upload_request()

		self.assertEqual([part["part_number"] for part in request["rootfs"]["parts"]], [1, 2])
		self.assertEqual([part["part_number"] for part in request["kernel"]["parts"]], [1])
		self.assertTrue(
			all(
				call.kwargs["expiry_seconds"] == 86400
				for call in object_storage_client.sign_upload_part.call_args_list
			)
		)

	def test_successful_transfer_marks_image_available_and_deletes_staging(self) -> None:
		image = SimpleNamespace(
			name="image-1",
			image_sha256="a" * 64,
			kernel_sha256="b" * 64,
			source_server="server-1",
			status="Failed",
			transfer_error="old error",
			save=Mock(),
		)
		server = SimpleNamespace(name="server-1")
		metal_client = Mock()
		settings = SimpleNamespace(get_object_storage_client=Mock(return_value=Mock()))
		service = VirtualMachineImageTransferService()

		with (
			patch("atlas.vm.core.vm_image_transfer.frappe.get_doc", return_value=server),
			patch("atlas.vm.core.vm_image_transfer.frappe.get_single", return_value=settings),
			patch("atlas.vm.core.vm_image_transfer.MetalClient", return_value=metal_client),
			patch.object(MultipartUploadService, "complete_stored_uploads"),
		):
			service.advance(image)

		metal_client.delete_snapshot.assert_called_once_with("image-1")
		self.assertEqual(image.status, "Available")
		self.assertIsNone(image.transfer_error)

	def test_completed_upload_status_records_sha256_and_finalizes(self) -> None:
		image = SimpleNamespace(
			name="image-1",
			image_sha256=None,
			kernel_sha256=None,
			source_server="server-1",
			status="Uploading",
			transfer_error=None,
			save=Mock(),
		)
		metal_client = Mock()
		metal_client.get_snapshot.return_value = {
			"state": "completed",
			"rootfs": {"sha256": "a" * 64},
			"kernel": {"sha256": "b" * 64},
		}
		settings = SimpleNamespace(get_object_storage_client=Mock(return_value=Mock()))
		service = VirtualMachineImageTransferService()

		with (
			patch(
				"atlas.vm.core.vm_image_transfer.frappe.get_doc",
				return_value=SimpleNamespace(name="server-1"),
			),
			patch("atlas.vm.core.vm_image_transfer.frappe.get_single", return_value=settings),
			patch("atlas.vm.core.vm_image_transfer.MetalClient", return_value=metal_client),
			patch.object(MultipartUploadService, "complete_stored_uploads"),
		):
			# First poll records the checksums; finalize runs on the next poll.
			service.advance(image)
			self.assertEqual(image.image_sha256, "a" * 64)
			self.assertEqual(image.kernel_sha256, "b" * 64)
			self.assertEqual(image.status, "Completing")
			metal_client.delete_snapshot.assert_not_called()

			# Next poll finalizes, deletes the snapshot, and marks it available.
			service.advance(image)

		metal_client.delete_snapshot.assert_called_once_with("image-1")
		self.assertEqual(image.status, "Available")

	def test_pending_upload_status_starts_the_upload(self) -> None:
		image = SimpleNamespace(
			name="image-1",
			image_sha256=None,
			kernel_sha256=None,
			source_server="server-1",
		)
		metal_client = Mock()
		metal_client.get_snapshot.return_value = {"state": "pending"}
		settings = SimpleNamespace(get_object_storage_client=Mock(return_value=Mock()))
		service = VirtualMachineImageTransferService()

		with (
			patch(
				"atlas.vm.core.vm_image_transfer.frappe.get_doc",
				return_value=SimpleNamespace(name="server-1"),
			),
			patch("atlas.vm.core.vm_image_transfer.frappe.get_single", return_value=settings),
			patch("atlas.vm.core.vm_image_transfer.MetalClient", return_value=metal_client),
			patch.object(service, "start_upload") as start_upload,
		):
			service.advance(image)

		start_upload.assert_called_once()
		metal_client.delete_snapshot.assert_not_called()

	def test_transfer_failure_keeps_identifiers_and_sets_actionable_error(self) -> None:
		image = SimpleNamespace(name="image-1", status="Uploading", transfer_error=None, save=Mock())
		service = VirtualMachineImageTransferService()

		with (
			patch("atlas.vm.core.vm_image_transfer.frappe.get_doc", return_value=image),
			patch("atlas.vm.core.vm_image_transfer.frappe.log_error"),
			patch.object(service, "advance", side_effect=ObjectStorageError("upload failed")),
		):
			service.transfer("image-1")

		self.assertEqual(image.status, "Failed")
		self.assertEqual(image.transfer_error, "upload failed")

	def test_upload_start_error_is_visible_and_keeps_retry_identifiers(self) -> None:
		image = SimpleNamespace(
			name="image-1",
			status="Pending",
			transfer_progress=0,
			transfer_error=None,
			rootfs_multipart_upload_id=None,
			kernel_multipart_upload_id=None,
			image_object_key="images/image/rootfs.img",
			kernel_object_key="images/image/kernel",
			image_size_mib=1,
			kernel_size_mib=1,
			save=Mock(),
		)
		metal_client = Mock()
		metal_client.start_snapshot_upload.side_effect = MetalClientError(
			"upload failed", status=500, code="upload_failed", retryable=True
		)
		object_storage_client = Mock()
		object_storage_client.create_multipart_upload.side_effect = ["rootfs-upload", "kernel-upload"]
		service = VirtualMachineImageTransferService()

		with self.assertRaises(MetalClientError):
			service.start_upload(
				image,
				metal_client,
				MultipartUploadService(image, object_storage_client),
			)

		self.assertEqual(image.rootfs_multipart_upload_id, "rootfs-upload")
		self.assertEqual(image.kernel_multipart_upload_id, "kernel-upload")
		image.save.assert_called_once_with()
