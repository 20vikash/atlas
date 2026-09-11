from __future__ import annotations

from pathlib import Path
from typing import TYPE_CHECKING, cast

import frappe
from frappe import _

from atlas.atlas.core.background_jobs import run_as_admin
from atlas.atlas.core.exceptions import AtlasUserError
from atlas.atlas.object_storage import ObjectStorageError

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings
	from atlas.atlas.object_storage import ObjectStorageClient
	from atlas.vm.doctype.virtual_machine_image.virtual_machine_image import (
		Artifact,
		VirtualMachineImage,
	)

MIGRATION_TIMEOUT_SECONDS = 3600


class VirtualMachineImageStorageMigration:
	"""Move one bootstrap System image into object storage."""

	def request(self, image: VirtualMachineImage) -> None:
		"""Queue one repeatable migration for a site file image."""
		if not image.is_stored_in_site_file:
			frappe.throw(_("This image is already in object storage."), exc=AtlasUserError)
		if image.status != "Available":
			frappe.throw(_("Only an Available image can be migrated."), exc=AtlasUserError)

		self.enqueue(cast(str, image.name))

	def enqueue(self, image_name: str) -> None:
		"""Enqueue one repeatable storage migration."""
		frappe.enqueue(
			"atlas.vm.core.vm_image_storage_migration.migrate_virtual_machine_image_storage",
			queue="long",
			timeout=MIGRATION_TIMEOUT_SECONDS,
			image_name=image_name,
			job_id=f"atlas||system-image||storage-migration||{image_name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)

	def migrate(self, image_name: str) -> None:
		"""Upload both artifacts, update the record, and remove the site files."""
		image = cast("VirtualMachineImage", frappe.get_doc("Virtual Machine Image", image_name))
		if not image.is_stored_in_site_file:
			return

		client = cast("AtlasSettings", frappe.get_single("Atlas Settings")).get_object_storage_client()
		replaced_files = (image.image_file, image.kernel_file)
		image.image_object_key = self.upload(client, image, "rootfs")
		image.kernel_object_key = self.upload(client, image, "kernel")
		image.image_file = None
		image.kernel_file = None
		image.artifact_storage = "Object Storage"
		image.save()
		frappe.db.commit()  # nosemgrep

		for file_name in replaced_files:
			if file_name:
				frappe.delete_doc("File", file_name, ignore_permissions=True, delete_permanently=True)

	def upload(self, client: ObjectStorageClient, image: VirtualMachineImage, artifact: Artifact) -> str:
		"""Upload one artifact under its content addressed key and verify its size."""
		if artifact == "rootfs":
			file_name, sha256 = image.image_file, cast(str, image.image_sha256)
		else:
			file_name, sha256 = image.kernel_file, cast(str, image.kernel_sha256)

		source = Path(frappe.get_doc("File", file_name).get_full_path())
		key = f"vm-images/sha256/{sha256}/{source.name.removeprefix(f'{sha256[:12]}-')}"
		client.upload_file(str(source), key)
		self.validate_stored_size(client, key, source.stat().st_size)
		return key

	@staticmethod
	def validate_stored_size(client: ObjectStorageClient, key: str, expected_bytes: int) -> None:
		"""Reject a stored object whose size does not match the local file."""
		metadata = client.head_object(key)
		stored_bytes = metadata.get("ContentLength") if metadata else None
		if stored_bytes != expected_bytes:
			raise ObjectStorageError(
				f"Stored object {key} is {stored_bytes} bytes and the local file is {expected_bytes} bytes"
			)


def enqueue_site_file_image_migrations() -> None:
	"""Queue migrations for Available images stored as site files."""
	settings = cast("AtlasSettings", frappe.get_single("Atlas Settings"))
	if not settings.is_object_storage_configured:
		return

	migration = VirtualMachineImageStorageMigration()
	for name in frappe.get_all(
		"Virtual Machine Image",
		filters={"artifact_storage": "Site File", "status": "Available"},
		pluck="name",
	):
		migration.enqueue(name)


@run_as_admin
def migrate_virtual_machine_image_storage(image_name: str) -> None:
	"""Run one queued storage migration and record a visible failure."""
	try:
		VirtualMachineImageStorageMigration().migrate(image_name)
	except ObjectStorageError as error:
		frappe.db.set_value("Virtual Machine Image", image_name, "transfer_error", str(error)[:1000])
		frappe.log_error(
			title=f"System image storage migration failed for {image_name}",
			message=frappe.get_traceback(),
		)
