# Copyright (c) 2026, Frappe and Contributors
# See license.txt

from __future__ import annotations

import json
import re
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

import atlas
import atlas.service.core.cargo.storage_cluster as storage_cluster
from atlas.service.core.cargo.storage_cluster import (
	remove_storage_cluster_config,
	storage_cluster_config_json,
	store_storage_cluster_config,
)

VALID_CONFIG = {
	"storage_node_count": 3,
	"replication_factor": 3,
	"gateway": {"cpu_millicores": 2000, "ram_gb": 4, "disk_gb": 20},
	"storage": {"cpu_millicores": 4000, "ram_gb": 8, "disk_gb": 500},
}


class TestStorageClusterConfig(UnitTestCase):
	def setUp(self) -> None:
		site_directory = TemporaryDirectory()
		self.addCleanup(site_directory.cleanup)
		patcher = patch.object(
			storage_cluster.frappe,
			"get_site_path",
			side_effect=lambda *parts: str(Path(site_directory.name).joinpath(*parts)),
		)
		patcher.start()
		self.addCleanup(patcher.stop)

	def test_a_stored_cluster_is_read_back_as_compact_json(self) -> None:
		store_storage_cluster_config(VALID_CONFIG)

		self.assertEqual(json.loads(storage_cluster_config_json()), VALID_CONFIG)
		self.assertNotIn(" ", storage_cluster_config_json())

	def test_installation_fails_loudly_without_a_stored_cluster(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "No storage cluster configuration"):
			storage_cluster_config_json()

	def test_an_archived_cluster_is_forgotten(self) -> None:
		store_storage_cluster_config(VALID_CONFIG)
		remove_storage_cluster_config()
		remove_storage_cluster_config()

		self.assertFalse(storage_cluster.config_file().exists())

	def test_fewer_storage_nodes_than_copies_is_rejected(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "at least the replication_factor of 3"):
			store_storage_cluster_config(VALID_CONFIG | {"storage_node_count": 2})

	def test_a_count_below_one_is_rejected(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "replication_factor must be a whole number"):
			store_storage_cluster_config(VALID_CONFIG | {"replication_factor": 0})

	def test_a_node_size_needs_every_field(self) -> None:
		with self.assertRaisesRegex(
			frappe.ValidationError, "storage must hold cpu_millicores, ram_gb and disk_gb"
		):
			store_storage_cluster_config(VALID_CONFIG | {"storage": {"cpu_millicores": 4000, "ram_gb": 8}})

	def test_a_node_size_below_one_is_rejected(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "gateway disk_gb must be a whole number"):
			store_storage_cluster_config(
				VALID_CONFIG | {"gateway": {"cpu_millicores": 2000, "ram_gb": 4, "disk_gb": 0}}
			)

	def test_a_node_cpu_outside_the_atlas_range_is_rejected(self) -> None:
		for cpu_millicores in (50, 40_000):
			with self.assertRaisesRegex(frappe.ValidationError, "cpu_millicores must be between"):
				store_storage_cluster_config(
					VALID_CONFIG | {"gateway": {"cpu_millicores": cpu_millicores, "ram_gb": 4, "disk_gb": 20}}
				)

	def test_a_missing_cluster_request_is_rejected(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "must be an object"):
			store_storage_cluster_config(None)

	def test_the_installer_writes_the_stored_cluster_to_site_config(self) -> None:
		script = (Path(atlas.__file__).parent / "scripts" / "install-cargo.sh").read_text()

		self.assertRegex(
			script,
			re.compile(r"set-config -p default_storage_cluster_config \$q_cluster_config"),
		)
		self.assertIn("for name in $ENROLMENT_VARS DEFAULT_STORAGE_CLUSTER_CONFIG; do", script)
