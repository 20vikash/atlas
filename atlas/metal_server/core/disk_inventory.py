from __future__ import annotations

import json
from collections import deque
from typing import TYPE_CHECKING

import frappe
from frappe import _

from atlas.atlas.doctype.ssh_task.ssh_task import SSHTask

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer

LOOP_DEVICE_TYPE = "loop"
WHOLE_DISK_TYPES = frozenset({"disk", "raid0", "raid1", "raid5", "raid6", "raid10"})


class DiskInventory:
	"""Read and parse the block devices for one server."""

	def __init__(self, server: "MetalServer") -> None:
		self.server = server

	def sync(self) -> None:
		"""Replace the Server disk rows with the current host inventory."""
		result = SSHTask.create_for_command(
			target_type=self.server.doctype,
			target=self.server.name,
			command="lsblk --json --bytes --paths --output NAME,TYPE,UUID,SIZE,MOUNTPOINT",
			run_in_background=False,
		).result
		if not result or not result.is_success:
			frappe.throw(_("Could not read the disks of server {0}.").format(self.server.name))

		self.server.set("disks", self.parse(result.output))
		self.server.save()

	def parse(self, lsblk_output: str) -> list[dict[str, str]]:
		"""Return whole disks and mounted devices, except loop devices."""
		devices = deque(json.loads(lsblk_output).get("blockdevices", []))
		disks: dict[str, dict[str, str]] = {}
		while devices:
			device = devices.popleft()
			devices.extendleft(reversed(device.get("children") or []))
			name = device["name"]
			if device.get("type") == LOOP_DEVICE_TYPE:
				continue
			if not device.get("mountpoint") and device.get("type") not in WHOLE_DISK_TYPES:
				continue
			disks[name] = {
				"device": name,
				"uuid": device.get("uuid") or "",
				"mount_point": device.get("mountpoint") or "",
				"size_gb": f"{int(device.get('size') or 0) / 1024**3:.2f}",
			}
		return list(disks.values())
