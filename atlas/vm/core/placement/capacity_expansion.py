from __future__ import annotations

import frappe

from atlas.atlas.core.background_jobs import run_as_admin
from atlas.vm.core.placement.models import PlacementRequirements


def enqueue_capacity_expansion(requirements: PlacementRequirements) -> None:
	"""Queue capacity expansion independently from the rejected VM request."""
	try:
		settings = frappe.get_single("Atlas Settings")
		if not settings.auto_spawn_metal_server:
			return

		# One pool needs one kind of host.
		is_sleepy = bool(requirements.is_sleepy and settings.use_dedicated_sleepy_vm_hosts)
		job_id = f"atlas||capacity-expansion||{requirements.architecture}||{int(is_sleepy)}"
		frappe.enqueue(
			ensure_capacity,
			architecture=requirements.architecture,
			memory_mib=requirements.memory_mib,
			disk_mib=requirements.disk_mib,
			is_sleepy=is_sleepy,
			job_id=job_id,
			deduplicate=True,
			enqueue_after_commit=False,
		)
	except Exception:
		frappe.logger("capacity-expansion").exception("Failed to queue Metal Server capacity expansion")


@run_as_admin
def ensure_capacity(architecture: str, memory_mib: int, disk_mib: int, *, is_sleepy: bool) -> None:
	"""Provision one host unless a matching host is already starting."""
	settings = frappe.get_single("Atlas Settings")
	if not settings.auto_spawn_metal_server:
		return

	size_name = settings.default_metal_machine_size
	image_name = settings.default_metal_machine_image
	if not size_name or not frappe.db.exists("Metal Server Size", size_name):
		frappe.log_error("Default Metal Machine Size is not available.", "Capacity expansion failed")
		return
	if not image_name or not frappe.db.exists("Metal Server Image", image_name):
		frappe.log_error("Default Metal Machine Image is not available.", "Capacity expansion failed")
		return

	size = frappe.get_doc("Metal Server Size", size_name)
	image = frappe.get_doc("Metal Server Image", image_name)
	if (
		not size.enabled
		or size.architecture != architecture
		or size.memory_mib < memory_mib
		or size.disk_gib * 1024 < disk_mib
		or not image.enabled
	):
		frappe.log_error(
			"Default Metal Machine Size or Image cannot provide the requested capacity.",
			"Capacity expansion failed",
		)
		return

	lock_key = f"{frappe.db.cur_db_name}:ensure-capacity:{architecture}:{int(is_sleepy)}"
	with frappe.db.advisory_lock(lock_key):
		# A waiter must not reuse a REPEATABLE READ snapshot from before the lock holder committed.
		frappe.db.rollback()
		pending = frappe.db.exists(
			"Metal Server",
			{
				"server_size": size.name,
				"architecture": architecture,
				"is_sleepy_vm_host": int(is_sleepy),
				"status": ["in", ["Pending", "Installing"]],
			},
		)
		if pending:
			return

		from atlas.metal_server.doctype.metal_server.metal_server import MetalServer

		MetalServer.provision(size=size.name, image=image.name, is_sleepy_vm_host=is_sleepy)
		frappe.db.commit()  # nosemgrep
