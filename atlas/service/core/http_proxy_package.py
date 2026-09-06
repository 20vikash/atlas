from __future__ import annotations

import hashlib
import io
import subprocess
import tarfile
from pathlib import Path

import frappe
from frappe import _

from atlas.atlas.core.artifacts import publish_public_file

COMPONENT_DIRECTORY = "services/http-proxy"
ARCHIVE_ROOT = "http-proxy"
PACKAGE_FILE_NAME = "http-proxy.tar"
PACKAGE_LABEL = "HTTP proxy package"
SETTINGS_FILE_FIELD = "http_proxy_package_file"
SETTINGS_HASH_FIELD = "http_proxy_package_hash"


def publish_http_proxy_package() -> None:
	"""Publish the component when its sources changed. Runs from `after_migrate`."""
	archive = build_archive()
	digest = hashlib.sha256(archive).hexdigest()
	if is_published(digest):
		print(f"atlas: {PACKAGE_LABEL} is current, skipped the build")
		return

	file_name = publish_package(archive, digest)
	print(f"atlas: published {PACKAGE_LABEL} as File {file_name}")


def publish_package(archive: bytes, digest: str) -> str:
	"""Attach the component archive as a File and link it in Atlas Settings."""
	file_name = publish_public_file(PACKAGE_FILE_NAME, PACKAGE_LABEL, archive)
	frappe.db.set_single_value("Atlas Settings", SETTINGS_FILE_FIELD, file_name)
	frappe.db.set_single_value("Atlas Settings", SETTINGS_HASH_FIELD, digest)
	return file_name


def is_published(digest: str) -> bool:
	"""Report whether the linked File already holds this archive."""
	settings = frappe.get_cached_doc("Atlas Settings")
	file_name = settings.get(SETTINGS_FILE_FIELD)
	if not file_name or settings.get(SETTINGS_HASH_FIELD) != digest:
		return False

	return bool(frappe.db.exists("File", file_name))


def build_archive() -> bytes:
	"""Return an uncompressed archive of the component, rooted at `http-proxy/`.

	The archive is reproducible: the entries are sorted, and each one carries a
	fixed timestamp, mode, and owner, so an unchanged tree gives the same bytes
	and its digest does not publish a second File.

	Only regular files go in, and every name is built here. A symbolic link, a
	device, or an absolute name could write outside the directory a host unpacks
	into.
	"""
	component = component_path()
	buffer = io.BytesIO()
	with tarfile.open(fileobj=buffer, mode="w", format=tarfile.PAX_FORMAT) as archive:
		for path in source_paths():
			content = path.read_bytes()
			info = tarfile.TarInfo(f"{ARCHIVE_ROOT}/{path.relative_to(component)}")
			info.size = len(content)
			info.mtime = 0
			info.mode = 0o755 if path.stat().st_mode & 0o100 else 0o644
			info.uid = info.gid = 0
			info.uname = info.gname = "root"
			archive.addfile(info, io.BytesIO(content))

	return buffer.getvalue()


def source_paths() -> list[Path]:
	"""Return every component file that belongs in the archive, in a stable order.

	Git decides what belongs, because it already reads `.gitignore`. A cache, a
	build directory, or a local environment never reaches a host.
	"""
	component = component_path()
	result = subprocess.run(
		["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
		cwd=component,
		capture_output=True,
		check=False,
	)
	if result.returncode != 0:
		frappe.throw(
			_("Cannot list the HTTP proxy sources: {0}").format(result.stderr.decode(errors="replace"))
		)

	paths = [component / name for name in result.stdout.decode().split("\0") if name]
	return sorted(path for path in paths if path.is_file() and not path.is_symlink())


def component_path() -> Path:
	"""Return the directory that holds the HTTP proxy component."""
	return Path(frappe.get_app_path("atlas")).parent / COMPONENT_DIRECTORY
