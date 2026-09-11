from __future__ import annotations

import hashlib
import os
import subprocess
import threading
from pathlib import Path
from typing import TYPE_CHECKING, Literal

import click
import frappe

from atlas.atlas.core.artifacts import publish_public_file_path
from atlas.vm.core.multipart_upload import bytes_to_mib

if TYPE_CHECKING:
	from atlas.atlas.object_storage import ObjectStorageClient

ArtifactStorage = Literal["Object Storage", "Site File"]


def build_ubuntu_image(
	version: str, platform: str, minimal: bool, output_directory: Path
) -> tuple[Path, Path]:
	"""Build one Ubuntu root file system and kernel."""
	output_directory.mkdir(parents=True, exist_ok=True)
	image_type = "minimal-" if minimal else ""
	image_path = output_directory / f"ubuntu-{version}-{image_type}{platform}.ext4"
	kernel_path = output_directory / f"vmlinux-ubuntu-{version}-{image_type}server"
	builder_path = Path(__file__).parents[1] / "scripts" / "build_ubuntu_server_image.sh"
	command = [builder_path]
	if os.geteuid() != 0:
		command.insert(0, "sudo")
	command.extend(
		[
			"--output",
			image_path,
			"--kernel-output",
			kernel_path,
			"--platform",
			platform,
			"--version",
			version,
			"--minimal" if minimal else "",
		]
	)
	subprocess.run([argument for argument in command if argument], check=True)
	return image_path, kernel_path


def publish_ubuntu_image(
	title: str,
	version: str,
	platform: str,
	image_path: Path,
	kernel_path: Path,
	storage: ArtifactStorage = "Object Storage",
) -> None:
	"""Publish Ubuntu artifacts and create or update their image record."""
	image_sha256 = get_sha256(image_path)
	kernel_sha256 = get_sha256(kernel_path)
	existing_name = frappe.db.exists("Virtual Machine Image", {"title": title})
	if existing_name:
		existing = frappe.get_doc("Virtual Machine Image", existing_name)
		if existing.image_sha256 == image_sha256 and existing.kernel_sha256 == kernel_sha256:
			return

	if storage == "Site File":
		location = publish_to_site_files(image_path, image_sha256, kernel_path, kernel_sha256)
	else:
		location = upload_to_object_storage(image_path, image_sha256, kernel_path, kernel_sha256)

	file_values = {
		"status": "Available",
		"artifact_storage": storage,
		"image_object_key": None,
		"kernel_object_key": None,
		"image_file": None,
		"kernel_file": None,
		"image_sha256": image_sha256,
		"image_size_mib": bytes_to_mib(image_path.stat().st_size),
		"kernel_sha256": kernel_sha256,
		"kernel_size_mib": bytes_to_mib(kernel_path.stat().st_size),
		**location,
	}
	if existing_name:
		replaced_files = (existing.image_file, existing.kernel_file)
		existing.update(file_values)
		existing.version = (existing.version or 1) + 1
		existing.save()

		for file_name in replaced_files:
			if file_name and file_name not in (existing.image_file, existing.kernel_file):
				frappe.delete_doc("File", file_name, ignore_permissions=True, delete_permanently=True)
		return

	frappe.get_doc(
		{
			"doctype": "Virtual Machine Image",
			"title": title,
			"version": 1,
			"image_type": "System",
			"platform": platform,
			"operating_system": "Ubuntu",
			"operating_system_version": version,
			"supports_cloud_init": 1,
			**file_values,
		}
	).insert()


def upload_to_object_storage(
	image_path: Path, image_sha256: str, kernel_path: Path, kernel_sha256: str
) -> dict[str, str]:
	"""Upload both artifacts under their content addressed keys."""
	image_key = f"vm-images/sha256/{image_sha256}/{image_path.name}"
	kernel_key = f"vm-images/sha256/{kernel_sha256}/{kernel_path.name}"
	settings = frappe.get_single("Atlas Settings")
	object_storage_client = settings.get_object_storage_client()
	upload_with_progress(object_storage_client, image_path, image_key)
	upload_with_progress(object_storage_client, kernel_path, kernel_key)
	return {"image_object_key": image_key, "kernel_object_key": kernel_key}


def publish_to_site_files(
	image_path: Path, image_sha256: str, kernel_path: Path, kernel_sha256: str
) -> dict[str, str]:
	"""Attach both artifacts as public site files for a host to download."""
	click.echo(f"Publishing {image_path.name} and {kernel_path.name} as public site files")
	return {
		"image_file": publish_public_file_path(image_path, image_sha256),
		"kernel_file": publish_public_file_path(kernel_path, kernel_sha256),
	}


def get_sha256(path: Path) -> str:
	"""Return the SHA-256 value for one file."""
	digest = hashlib.sha256()
	with path.open("rb") as source:
		for chunk_data in iter(lambda: source.read(1024 * 1024), b""):
			digest.update(chunk_data)
	return digest.hexdigest()


def upload_with_progress(object_storage_client: ObjectStorageClient, source: Path, key: str) -> None:
	"""Upload a file to object storage and show its progress."""
	total_bytes = source.stat().st_size
	transferred_bytes = 0
	progress_lock = threading.Lock()

	def on_progress(chunk_bytes: int) -> None:
		"""Record build progress on the image record."""
		nonlocal transferred_bytes
		with progress_lock:
			transferred_bytes += chunk_bytes
			percentage = transferred_bytes / total_bytes * 100 if total_bytes else 100
			click.echo(
				f"\rUploading {source.name}: {transferred_bytes >> 20}/{total_bytes >> 20} MiB ({percentage:5.1f}%)",
				nl=False,
			)

	object_storage_client.upload_file(str(source), key, on_progress=on_progress)
	click.echo()
