from __future__ import annotations

from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
	from atlas.atlas.object_storage import ObjectStorageClient
	from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage

MEBIBYTE = 1 << 20
MULTIPART_PART_SIZE_MIB = 2 * 1024


class MultipartUploadError(Exception):
	"""Report invalid multipart upload data."""


class MultipartUploadService:
	"""Own multipart upload state and object storage validation for one image."""

	def __init__(self, image: VirtualMachineImage, object_storage_client: ObjectStorageClient) -> None:
		self.image = image
		self.object_storage_client = object_storage_client

	def ensure_uploads(self, *, save: bool = True) -> None:
		"""Create missing multipart uploads and optionally save the image."""
		if not self.image.rootfs_multipart_upload_id:
			object_key = self.require_value(self.image.image_object_key, "rootfs object key")
			self.image.rootfs_multipart_upload_id = self.object_storage_client.create_multipart_upload(
				object_key
			)

		if not self.image.kernel_multipart_upload_id:
			object_key = self.require_value(self.image.kernel_object_key, "kernel object key")
			self.image.kernel_multipart_upload_id = self.object_storage_client.create_multipart_upload(
				object_key
			)

		if save:
			self.image.save(ignore_permissions=True)

	def get_upload_request(self) -> dict[str, Any]:
		"""Return signed upload parts for each image artifact."""
		return {
			"rootfs": {
				"parts": self.get_signed_parts(
					self.require_value(self.image.image_object_key, "rootfs object key"),
					self.require_value(self.image.rootfs_multipart_upload_id, "rootfs upload ID"),
					self.image.image_size_mib,
				)
			},
			"kernel": {
				"parts": self.get_signed_parts(
					self.require_value(self.image.kernel_object_key, "kernel object key"),
					self.require_value(self.image.kernel_multipart_upload_id, "kernel upload ID"),
					self.image.kernel_size_mib,
				)
			},
		}

	def get_signed_parts(self, object_key: str, upload_id: str, size_mib: int) -> list[dict[str, int | str]]:
		"""Return one signed URL for each required multipart upload part."""
		return [
			{
				"part_number": part_number,
				"url": self.object_storage_client.sign_upload_part(
					object_key, upload_id, part_number, expiry_seconds=86400
				),
			}
			for part_number in range(1, get_multipart_part_count(size_mib) + 1)
		]

	def complete_stored_uploads(self) -> None:
		"""Validate and complete both stored image artifact uploads."""
		parts_by_artifact = {
			"rootfs": self.get_stored_parts(
				self.require_value(self.image.image_object_key, "rootfs object key"),
				self.require_value(self.image.rootfs_multipart_upload_id, "rootfs upload ID"),
				self.image.image_size_mib,
			),
			"kernel": self.get_stored_parts(
				self.require_value(self.image.kernel_object_key, "kernel object key"),
				self.require_value(self.image.kernel_multipart_upload_id, "kernel upload ID"),
				self.image.kernel_size_mib,
			),
		}
		self.complete_uploads(parts_by_artifact)

	def get_stored_parts(self, object_key: str, upload_id: str, size_mib: int) -> list[dict[str, Any]]:
		"""Return and validate the parts stored for one artifact."""
		head = self.object_storage_client.head_object(object_key)
		if head and self.has_expected_size(head, size_mib):
			return []
		parts = self.object_storage_client.list_multipart_parts(object_key, upload_id)
		self.validate_parts("stored", size_mib, parts)
		return parts

	def complete_uploads(self, parts_by_artifact: dict[str, list[dict[str, Any]]]) -> None:
		"""Complete both multipart uploads."""
		self.complete_upload(
			self.require_value(self.image.image_object_key, "rootfs object key"),
			self.require_value(self.image.rootfs_multipart_upload_id, "rootfs upload ID"),
			self.image.image_size_mib,
			parts_by_artifact["rootfs"],
		)
		self.complete_upload(
			self.require_value(self.image.kernel_object_key, "kernel object key"),
			self.require_value(self.image.kernel_multipart_upload_id, "kernel upload ID"),
			self.image.kernel_size_mib,
			parts_by_artifact["kernel"],
		)

	def complete_upload(
		self, object_key: str, upload_id: str, size_mib: int, parts: list[dict[str, Any]]
	) -> None:
		"""Complete one upload and verify the stored object size."""
		head = self.object_storage_client.head_object(object_key)
		if head and self.has_expected_size(head, size_mib):
			return
		self.object_storage_client.complete_multipart_upload(object_key, upload_id, parts)
		head = self.object_storage_client.head_object(object_key)
		if not head or not self.has_expected_size(head, size_mib):
			raise MultipartUploadError(f"Object storage stored an invalid size for {object_key}")

	@staticmethod
	def validate_parts(artifact: str, size_mib: int, parts: list[dict[str, Any]]) -> None:
		"""Reject missing, unordered, or empty multipart upload parts."""
		expected_numbers = list(range(1, get_multipart_part_count(size_mib) + 1))
		part_numbers = [part.get("part_number", part.get("PartNumber")) for part in parts]
		if part_numbers != expected_numbers:
			raise MultipartUploadError(f"Metal returned invalid {artifact} part numbers")
		if any(not part.get("etag", part.get("ETag")) for part in parts):
			raise MultipartUploadError(f"Metal returned an empty {artifact} ETag")

	@staticmethod
	def has_expected_size(head: dict[str, Any], size_mib: int) -> bool:
		"""Return whether a stored object has the expected rounded size."""
		content_length = head.get("ContentLength")
		return isinstance(content_length, int) and bytes_to_mib(content_length) == size_mib

	@staticmethod
	def require_value(value: str | None, label: str) -> str:
		"""Return one required upload value."""
		if not value:
			raise MultipartUploadError(f"Machine image has no {label}")
		return value


def bytes_to_mib(size_bytes: int) -> int:
	"""Return the size in MiB, rounded up to the next whole MiB."""
	return (size_bytes + MEBIBYTE - 1) // MEBIBYTE


def get_multipart_part_count(size_mib: int) -> int:
	"""Return the required multipart upload part count."""
	if size_mib <= 0:
		raise MultipartUploadError("Artifact size must be positive")
	part_count = (size_mib + MULTIPART_PART_SIZE_MIB - 1) // MULTIPART_PART_SIZE_MIB
	if part_count > 10_000:
		raise MultipartUploadError("Artifact needs more than 10,000 multipart upload parts")
	return part_count
