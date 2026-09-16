# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import frappe
from frappe import _

from atlas.vm.core.models import MAXIMUM_CPU_MILLICORES, MINIMUM_CPU_MILLICORES

CONFIG_FILE_PATH = ("private", "files", "cargo-storage-cluster.json")
NODE_ROLES = ("gateway", "storage")
NODE_FIELDS = ("cpu_millicores", "ram_gb", "disk_gb")


def config_file() -> Path:
	"""Return the file that holds the requested cluster between provision and installation."""
	return Path(frappe.get_site_path(*CONFIG_FILE_PATH))


def store_storage_cluster_config(values: Any) -> dict[str, Any]:
	"""Validate the requested cluster and keep it for the installer. Throws on a bad request."""
	config = _validated_config(values)
	path = config_file()
	path.parent.mkdir(parents=True, exist_ok=True)
	path.write_text(json.dumps(config, indent=2))
	return config


def storage_cluster_config_json() -> str:
	"""Return the stored cluster as the compact JSON the installer passes to Cargo."""
	path = config_file()
	if not path.exists():
		frappe.throw(_("No storage cluster configuration was stored for this Cargo Server."))

	return json.dumps(json.loads(path.read_text()), separators=(",", ":"))


def remove_storage_cluster_config() -> None:
	"""Forget the cluster of an archived Cargo Server."""
	config_file().unlink(missing_ok=True)


def _validated_config(values: Any) -> dict[str, Any]:
	if not isinstance(values, dict):
		frappe.throw(_("The storage cluster configuration must be an object."))

	replication_factor = _whole_number(values.get("replication_factor"), "replication_factor")
	storage_node_count = _whole_number(values.get("storage_node_count"), "storage_node_count")
	if storage_node_count < replication_factor:
		frappe.throw(
			_("storage_node_count must be at least the replication_factor of {0}.").format(replication_factor)
		)

	return {
		"storage_node_count": storage_node_count,
		"replication_factor": replication_factor,
		**{role: _node_size(values.get(role), role) for role in NODE_ROLES},
	}


def _node_size(values: Any, role: str) -> dict[str, int]:
	if not isinstance(values, dict) or any(field not in values for field in NODE_FIELDS):
		frappe.throw(_("{0} must hold cpu_millicores, ram_gb and disk_gb.").format(role))

	size = {field: _whole_number(values[field], f"{role} {field}") for field in NODE_FIELDS}
	if not MINIMUM_CPU_MILLICORES <= size["cpu_millicores"] <= MAXIMUM_CPU_MILLICORES:
		frappe.throw(
			_("{0} cpu_millicores must be between {1} and {2}.").format(
				role, MINIMUM_CPU_MILLICORES, MAXIMUM_CPU_MILLICORES
			)
		)

	return size


def _whole_number(value: Any, name: str) -> int:
	if isinstance(value, bool) or not isinstance(value, int) or value < 1:
		frappe.throw(_("{0} must be a whole number of at least 1.").format(name))

	return value
