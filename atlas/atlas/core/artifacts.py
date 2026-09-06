from __future__ import annotations

import hashlib

import frappe


def publish_public_file(file_name: str, label: str, content: bytes) -> str:
	"""Insert a public File for one build and return its document name.

	Read its URL with `get_download_url`. Frappe adds a suffix when the stored
	name is still taken, so the URL is not stable across builds.
	"""
	file_doc = frappe.get_doc(
		{
			"doctype": "File",
			"file_name": file_name,
			"attached_to_doctype": "Atlas Settings",
			"attached_to_name": "Atlas Settings",
			"is_private": 0,
			"content": content,
		}
	).insert(ignore_permissions=True)

	print(f"atlas: {label} sha256 {hashlib.sha256(content).hexdigest()}")
	return file_doc.name


def delete_unlinked_files() -> None:
	"""Delete every Atlas Settings File that no Link field names any more.

	A build publishes a new File and moves the link to it. The File it replaced
	stays downloadable until this job runs, so a host that started an install
	with the older URL can still finish it.
	"""
	for file_name in get_unlinked_files():
		frappe.delete_doc("File", file_name, ignore_permissions=True, delete_permanently=True)


def get_unlinked_files() -> list[str]:
	"""Return every Atlas Settings File that no Link field names any more."""
	linked_files = get_linked_files()
	attached_files = frappe.get_all("File", filters={"attached_to_doctype": "Atlas Settings"}, pluck="name")
	return [file_name for file_name in attached_files if file_name not in linked_files]


def get_linked_files() -> set[str]:
	"""Return every File that an Atlas Settings Link field names."""
	settings = frappe.get_single("Atlas Settings")
	link_fields = settings.meta.get("fields", {"fieldtype": "Link", "options": "File"})
	return {value for field in link_fields if (value := settings.get(field.fieldname))}


def get_download_url(file_name: str) -> str:
	"""Return the URL a host uses to download one published File.

	`atlas_base_url` in the site configuration names an address that a host can
	reach, which the site's own URL is not during local development.
	"""
	file_url = frappe.db.get_value("File", file_name, "file_url")
	base_url = frappe.conf.atlas_base_url or frappe.utils.get_url()
	return f"{base_url.rstrip('/')}{file_url}"
