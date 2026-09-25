from __future__ import annotations

from datetime import datetime
from typing import TYPE_CHECKING, cast

import frappe
from frappe import _
from frappe.utils import get_datetime, now_datetime

from atlas.atlas.core.background_jobs import run_as_admin
from atlas.atlas.core.exceptions import AtlasUserError
from atlas.atlas.object_storage import ObjectStorageError
from atlas.vm.core.metal_client import MetalClient, MetalClientError
from atlas.vm.core.vm_state import has_live_virtual_machine_for_image

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings
	from atlas.atlas.object_storage import ObjectStorageClient
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer
	from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import VirtualMachineImage


DELETION_TIMEOUT_SECONDS = 900


class VirtualMachineImageInUse(AtlasUserError):
	"""Report that a virtual machine uses the image."""

	http_status_code = 409


class VirtualMachineImageDeletionService:
	"""Own the deletion of one image."""

	def request(self, image: VirtualMachineImage) -> str:
		"""Retire one image, and reclaim its artifacts when nothing needs them."""
		if image.status in ("Deleting", "Archived"):
			return cast(str, image.status)

		image.ensure_not_termination_protected()

		if image.status not in ("Available", "Failed"):
			frappe.throw(_("Only an Available or Failed image can be deleted."), exc=AtlasUserError)

		image.status = "Deleting" if self.is_reclaimable(image) else "Archived"
		image.enabled = 0
		image.save()

		if image.status == "Deleting":
			self.enqueue(cast(str, image.name))

		return cast(str, image.status)

	def reclaim_archived(self) -> None:
		"""Delete each archived image in object storage that lost its last virtual machine."""
		for image in frappe.get_all(
			"Virtual Machine Image",
			filters={"status": "Archived", "artifact_storage": "Object Storage"},
			fields=["name", "site_file_retention_until"],
		):
			if is_retaining_site_files(image.site_file_retention_until):
				continue
			if has_live_virtual_machine_for_image(image.name):
				continue

			frappe.db.set_value("Virtual Machine Image", image.name, "status", "Deleting")
			self.enqueue(image.name)

	@staticmethod
	def is_reclaimable(image: VirtualMachineImage) -> bool:
		"""Return whether the stored artifacts can be removed now.

		Site files serve bootstrap hosts, so only an image in object storage that no
		live virtual machine needs releases its storage.
		"""
		if image.artifact_storage != "Object Storage":
			return False
		if is_retaining_site_files(image.site_file_retention_until):
			return False

		return not has_live_virtual_machine_for_image(cast(str, image.name))

	def enqueue(self, image_name: str) -> None:
		"""Enqueue one repeatable image cleanup."""
		frappe.enqueue(
			"atlas.vm.core.vm_image_deletion.delete_virtual_machine_image",
			queue="long",
			timeout=DELETION_TIMEOUT_SECONDS,
			image_name=image_name,
			job_id=f"atlas||image||deletion||{image_name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)

	def delete(self, image_name: str) -> None:
		"""Remove stored objects, incomplete uploads, and staged Metal data."""
		image = cast("VirtualMachineImage", frappe.get_doc("Virtual Machine Image", image_name))
		self.validate_is_unused(image_name)
		client = cast("AtlasSettings", frappe.get_single("Atlas Settings")).get_object_storage_client()
		self.abort_uploads(image, client)
		self.delete_objects(image, client)
		self.delete_staged_snapshot(image)
		frappe.delete_doc("Virtual Machine Image", image_name)

	@staticmethod
	def validate_is_unused(image_name: str) -> None:
		"""Reject deletion while a live virtual machine needs the image."""
		if has_live_virtual_machine_for_image(image_name):
			frappe.throw(_("A live Virtual Machine uses this image."), exc=VirtualMachineImageInUse)

	@staticmethod
	def abort_uploads(image: VirtualMachineImage, client: ObjectStorageClient) -> None:
		"""Abort each multipart upload that the transfer did not complete."""
		for object_key, upload_id in (
			(image.image_object_key, image.rootfs_multipart_upload_id),
			(image.kernel_object_key, image.kernel_multipart_upload_id),
		):
			if object_key and upload_id:
				client.abort_multipart_upload(object_key, upload_id)

	@staticmethod
	def delete_objects(image: VirtualMachineImage, client: ObjectStorageClient) -> None:
		"""Remove both stored artifacts owned by this image."""
		for object_key in (image.image_object_key, image.kernel_object_key):
			if object_key:
				client.delete_object(object_key)

	@staticmethod
	def delete_staged_snapshot(image: VirtualMachineImage) -> None:
		"""Remove Metal staging data that a stopped transfer left behind."""
		if not image.source_server or not frappe.db.exists("Metal Server", image.source_server):
			return

		server = cast("MetalServer", frappe.get_doc("Metal Server", image.source_server))
		try:
			MetalClient(server).delete_snapshot(cast(str, image.name))
		except MetalClientError as error:
			if not error.is_not_found:
				raise


def is_retaining_site_files(site_file_retention_until: str | datetime | None) -> bool:
	"""Return whether a storage migration still keeps site files for hosts that download them."""
	return bool(site_file_retention_until) and get_datetime(site_file_retention_until) > now_datetime()


def enqueue_pending_virtual_machine_image_deletions() -> None:
	"""Resume unfinished deletions and reclaim an archived image that is now unused."""
	service = VirtualMachineImageDeletionService()
	for name in frappe.get_all(
		"Virtual Machine Image",
		filters={"status": "Deleting"},
		pluck="name",
	):
		service.enqueue(name)

	service.reclaim_archived()


@run_as_admin
def delete_virtual_machine_image(image_name: str) -> None:
	"""Run one queued image cleanup and record a visible failure."""
	try:
		VirtualMachineImageDeletionService().delete(image_name)
	except (MetalClientError, ObjectStorageError) as error:
		frappe.db.set_value("Virtual Machine Image", image_name, "transfer_error", str(error)[:1000])
		frappe.log_error(
			title=f"Image deletion failed for {image_name}",
			message=frappe.get_traceback(),
		)
