from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import patch

import frappe
from frappe.tests import IntegrationTestCase, UnitTestCase

from atlas.atlas.core import artifacts


class TestArtifacts(UnitTestCase):
	def test_download_url_prefers_the_configured_base(self) -> None:
		"""A host cannot reach the site URL of a local bench."""
		with (
			patch.object(artifacts.frappe.db, "get_value", return_value="/files/metald-linux-amd64"),
			patch.object(artifacts.frappe, "conf", SimpleNamespace(atlas_base_url="https://atlas.test/")),
		):
			url = artifacts.get_download_url("metald-file")

		self.assertEqual(url, "https://atlas.test/files/metald-linux-amd64")

	def test_download_url_falls_back_to_the_site_url(self) -> None:
		with (
			patch.object(artifacts.frappe.db, "get_value", return_value="/files/metald-linux-amd64"),
			patch.object(artifacts.frappe, "conf", SimpleNamespace(atlas_base_url=None)),
			patch.object(artifacts.frappe.utils, "get_url", return_value="http://atlas.localhost:8000"),
		):
			url = artifacts.get_download_url("metald-file")

		self.assertEqual(url, "http://atlas.localhost:8000/files/metald-linux-amd64")


class TestPublishedFiles(IntegrationTestCase):
	"""The sweep deletes files on disk, which no rollback undoes.

	These tests therefore assert on the list the sweep would act on, and check the
	deletion itself with a stubbed `delete_doc`.
	"""

	FILE_NAME = "atlas-test-artifact.tar.gz"
	SETTINGS_FIELD = "http_proxy_package_file"

	def setUp(self) -> None:
		self.previous_link = frappe.db.get_single_value("Atlas Settings", self.SETTINGS_FIELD)

	def tearDown(self) -> None:
		frappe.db.set_single_value("Atlas Settings", self.SETTINGS_FIELD, self.previous_link)
		for name in frappe.get_all("File", filters={"file_name": ("like", "atlas-test-%")}, pluck="name"):
			frappe.delete_doc("File", name, ignore_permissions=True, delete_permanently=True, force=True)

	def publish(self, content: bytes) -> str:
		"""Publish one artifact and link it, the way a build does."""
		file_name = artifacts.publish_public_file(self.FILE_NAME, "test artifact", content)
		frappe.db.set_single_value("Atlas Settings", self.SETTINGS_FIELD, file_name)
		return file_name

	def test_each_publish_makes_its_own_file(self) -> None:
		first = self.publish(b"first build")

		second = self.publish(b"second build")

		self.assertNotEqual(first, second)
		self.assertTrue(frappe.db.exists("File", first))
		self.assertTrue(frappe.db.exists("File", second))

	# The replaced file stays downloadable until the sweep, so an install that
	# started with the older URL can finish.
	def test_the_file_a_build_replaced_becomes_unlinked(self) -> None:
		first = self.publish(b"first build")
		second = self.publish(b"second build")

		unlinked_files = artifacts.get_unlinked_files()

		self.assertIn(first, unlinked_files)
		self.assertNotIn(second, unlinked_files)

	def test_a_linked_file_is_never_unlinked(self) -> None:
		current = self.publish(b"only build")

		self.assertNotIn(current, artifacts.get_unlinked_files())
		self.assertIn(current, artifacts.get_linked_files())

	def test_the_sweep_deletes_every_unlinked_file(self) -> None:
		with (
			patch.object(artifacts, "get_unlinked_files", return_value=["first", "second"]),
			patch.object(artifacts.frappe, "delete_doc") as delete_doc,
		):
			artifacts.delete_unlinked_files()

		self.assertEqual([call.args[1] for call in delete_doc.call_args_list], ["first", "second"])
