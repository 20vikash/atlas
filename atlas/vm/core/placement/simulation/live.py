"""Run a small, real-host placement trial beside an existing Atlas fleet.

Run from the bench root with a running scheduler and worker. Run each strategy
with the same arguments. Placement sees the ready site fleet. The host cap and
cleanup cover only trial resources. The report lists baseline resources too.
A successful run removes its VMs and hosts. An interrupted or failed run keeps
them for inspection and cleanup.
Repeat --tenant-id for each tenant. VM requests rotate across those IDs.
"""

from __future__ import annotations

import argparse
import json
import os
import time
from collections.abc import Callable, Sequence
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import TYPE_CHECKING
from unittest.mock import patch

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer
	from atlas.vm.core.models import VirtualMachineCreateRequest
	from atlas.vm.core.placement.context import PlacementContext


class HostLimitReached(RuntimeError):
	"""Stop a trial before it requests more provider hosts than allowed."""


def positive_integer(value: str) -> int:
	"""Parse a positive CLI integer."""
	parsed = int(value)
	if parsed < 1:
		raise argparse.ArgumentTypeError("must be positive")
	return parsed


def non_negative_integer(value: str) -> int:
	"""Parse a non-negative CLI integer."""
	parsed = int(value)
	if parsed < 0:
		raise argparse.ArgumentTypeError("must be non-negative")
	return parsed


def tenant_identifier(value: str) -> int:
	"""Parse one 32-bit tenant ID for a VM request."""
	parsed = non_negative_integer(value)
	if parsed > 0xFFFFFFFF:
		raise argparse.ArgumentTypeError("tenant ID must be at most 4294967295")
	return parsed


def non_negative_float(value: str) -> float:
	"""Parse a non-negative CLI duration."""
	parsed = float(value)
	if not 0 <= parsed < float("inf"):
		raise argparse.ArgumentTypeError("must be a finite non-negative number")
	return parsed


def request_sequence(
	regular_count: int, sleepy_count: int, tenant_ids: Sequence[int]
) -> list[tuple[bool, int]]:
	"""Interleave pools and assign each pool's VMs across the tenants."""
	if not tenant_ids:
		raise ValueError("At least one tenant ID is required.")
	sequence: list[tuple[bool, int]] = []
	for index in range(max(regular_count, sleepy_count)):
		if index < regular_count:
			sequence.append((False, tenant_ids[index % len(tenant_ids)]))
		if index < sleepy_count:
			sequence.append((True, tenant_ids[(regular_count + index) % len(tenant_ids)]))
	return sequence


def timestamp() -> str:
	"""Return the current UTC time for the trial report."""
	return datetime.now(UTC).isoformat()


def trial_placement_context(new_host_type: str) -> type[PlacementContext]:
	"""Use normal fleet placement with the trial's selected new host type."""
	from atlas.vm.core.placement.context import PlacementContext

	class TrialPlacementContext(PlacementContext):
		def spawn_host(
			self, host_type: str | None = None, *, count: int = 1, is_sleepy: bool = False
		) -> None:
			super().spawn_host(host_type or new_host_type, count=count, is_sleepy=is_sleepy)

	return TrialPlacementContext


class LiveTrial:
	"""Own the state and resource manifest of one live placement trial."""

	def __init__(self, arguments: argparse.Namespace, report: dict) -> None:
		self.arguments = arguments
		self.report = report
		self.output = arguments.output

	def save(self) -> None:
		"""Write a recoverable report before the next provider operation."""
		temporary = self.output.with_name(f"{self.output.name}.tmp")
		temporary.write_text(json.dumps(self.report, indent=2) + "\n")
		os.replace(temporary, self.output)

	def build_request(self, is_sleepy: bool, tenant_id: int) -> VirtualMachineCreateRequest:
		"""Validate one VM shape through the normal create request boundary."""
		from atlas.vm.core.models import VirtualMachineCreateRequest

		return VirtualMachineCreateRequest.from_value(
			{
				"virtual_machine_image": self.arguments.image,
				"cpu_millicores": self.arguments.cpu_millicores,
				"memory_mib": self.arguments.memory_mib,
				"disk_mib": self.arguments.disk_mib,
				"tenant_id": tenant_id,
				"egress": "none",
				"sleep_after_idle_seconds": self.arguments.sleep_after_idle_seconds if is_sleepy else 0,
			}
		)

	def snapshot(self) -> None:
		"""Update the state of resources created by this trial."""
		import frappe

		hosts = frappe.get_all(
			"Metal Server",
			filters={"status": ["!=", "Deleted"]},
			fields=["name", "status", "is_sleepy", "is_provisioning_completed"],
		)
		for host in hosts:
			if host.name not in self.report["hosts"]:
				continue
			previous = self.report["hosts"].get(host.name)
			current = {
				"status": host.status,
				"is_sleepy": bool(host.is_sleepy),
				"is_provisioning_completed": bool(host.is_provisioning_completed),
			}
			if previous != current:
				self.report["events"].append(
					{"at": timestamp(), "kind": "host_state", "name": host.name, **current}
				)
			self.report["hosts"][host.name] = current
		for vm in frappe.get_all("Virtual Machine", fields=["name", "server", "is_draft", "is_terminating"]):
			if vm.name not in self.report["virtual_machines"]:
				continue
			self.report["virtual_machines"][vm.name]["server"] = vm.server
			self.report["virtual_machines"][vm.name]["is_draft"] = bool(vm.is_draft)
		self.save()

	def preflight(self) -> None:
		"""Record existing resources and validate the catalog before spending money."""
		import frappe

		from atlas.vm.core.vm_service import VirtualMachineService

		self.report["baseline_hosts"] = frappe.get_all(
			"Metal Server", filters={"status": ["!=", "Deleted"]}, pluck="name"
		)
		self.report["baseline_virtual_machines"] = frappe.get_all("Virtual Machine", pluck="name")
		settings = frappe.get_single("Atlas Settings")
		if not settings.is_server_provider_setup_completed:
			raise ValueError("The server provider is not configured on this site.")
		if settings.server_provider == "Scaleway" and settings.scaleway_machine_billing_cycle != "Hourly":
			raise ValueError("Set Scaleway machine billing to Hourly for this short trial.")
		for tenant_id in self.arguments.tenant_ids:
			self.build_request(self.arguments.sleepy_count > 0, tenant_id)
		size = frappe.get_doc("Metal Server Size", self.arguments.host_type)
		image = VirtualMachineService.get_image(self.arguments.image, self.arguments.tenant_ids[0])
		for tenant_id in self.arguments.tenant_ids[1:]:
			VirtualMachineService.get_image(self.arguments.image, tenant_id)
		image.validate_compatibility(self.arguments.disk_mib)
		if not size.enabled or size.provider_type != settings.server_provider:
			raise ValueError("The host type is disabled or belongs to another provider.")
		if size.architecture != image.architecture:
			raise ValueError("The host type and VM image architectures differ.")
		if size.memory_mib < self.arguments.memory_mib or size.disk_gib * 1024 < self.arguments.disk_mib:
			raise ValueError("The host type cannot hold this VM shape.")
		self.save()
		frappe.db.rollback()

	def guarded_provision(self) -> Callable[..., MetalServer]:
		"""Return a host constructor that enforces the trial's provider host cap."""
		import frappe

		from atlas.metal_server.doctype.metal_server.metal_server import MetalServer

		original = MetalServer.provision

		def provision(*args, **kwargs):
			active_hosts = set(
				frappe.get_all("Metal Server", filters={"status": ["!=", "Deleted"]}, pluck="name")
			)
			new_count = len(active_hosts.intersection(self.report["hosts"]))
			if new_count >= self.arguments.max_hosts:
				raise HostLimitReached(f"Trial reached --max-hosts={self.arguments.max_hosts}.")
			server = original(*args, **kwargs)
			self.report["hosts"][server.name] = {
				"status": server.status,
				"is_sleepy": bool(server.is_sleepy),
				"is_provisioning_completed": bool(server.is_provisioning_completed),
			}
			self.report["events"].append({"at": timestamp(), "kind": "host_requested", "name": server.name})
			self.save()
			return server

		return provision

	def refresh_capacity(self) -> bool:
		"""Queue fresh usage samples and say whether placement must wait."""
		import frappe
		from frappe.utils import now_datetime

		from atlas.metal_server.usage import enqueue_server_sync

		has_fresh_host = False
		has_stale_host = False
		for name in frappe.get_all(
			"Metal Server",
			filters={"status": "Running", "is_provisioning_completed": 1},
			pluck="name",
		):
			if frappe.db.exists(
				"Metal Server Usage",
				{"server": name, "creation": [">=", now_datetime() - timedelta(minutes=2)]},
			):
				has_fresh_host = True
				continue
			enqueue_server_sync(name)
			has_stale_host = True
		return has_stale_host and not has_fresh_host

	def create_requests(self) -> None:
		"""Create each real VM, retrying only when a host is provisioning."""
		import frappe

		from atlas.metal_server.doctype.metal_server.metal_server import MetalServer
		from atlas.vm.core.placement.context import CapacityPending
		from atlas.vm.core.placement.service import PlacementService
		from atlas.vm.core.placement.strategies import STRATEGIES
		from atlas.vm.core.vm_service import VirtualMachineCreateError, VirtualMachineService

		placement_context = trial_placement_context(self.arguments.host_type)

		def select_trial_server(
			service: PlacementService,
			request: VirtualMachineCreateRequest,
			architecture: str,
			exclude_servers: set[str] | None = None,
		) -> MetalServer:
			if exclude_servers:
				raise RuntimeError("The live trial supports VM creation only.")
			settings = frappe.get_single("Atlas Settings")
			placement = placement_context(request, architecture, settings.sleepy_vm_overcommit_factor)
			STRATEGIES[self.arguments.strategy].select_host(placement)
			return placement.finish()

		deadline = time.monotonic() + self.arguments.timeout_seconds
		sequence = request_sequence(
			self.arguments.regular_count, self.arguments.sleepy_count, self.arguments.tenant_ids
		)
		with (
			patch.object(MetalServer, "provision", staticmethod(self.guarded_provision())),
			patch.object(PlacementService, "select_server", select_trial_server),
		):
			for index, (is_sleepy, tenant_id) in enumerate(sequence, start=1):
				started = time.monotonic()
				request = self.build_request(is_sleepy, tenant_id)
				while True:
					if time.monotonic() >= deadline:
						raise TimeoutError("Trial timed out while waiting for placement capacity.")
					if self.refresh_capacity():
						frappe.db.rollback()
						time.sleep(self.arguments.poll_seconds)
						continue
					try:
						result = VirtualMachineService.create(request)
					except CapacityPending:
						frappe.db.rollback()
						self.snapshot()
						frappe.db.rollback()
						time.sleep(self.arguments.poll_seconds)
						continue
					except VirtualMachineCreateError as error:
						frappe.db.rollback()
						self.report["virtual_machines"][error.virtual_machine_name] = {
							"created_at": timestamp(),
							"tenant_id": tenant_id,
							"is_draft": True,
						}
						self.save()
						self.snapshot()
						self.report["unresolved_vm"] = error.virtual_machine_name
						self.save()
						raise
					self.report["virtual_machines"][result["name"]] = {
						"created_at": timestamp(),
						"tenant_id": tenant_id,
						"is_draft": result["is_draft"],
					}
					self.save()
					frappe.db.commit()
					self.snapshot()
					self.report["events"].append(
						{
							"at": timestamp(),
							"kind": "vm_created",
							"name": result["name"],
							"tenant_id": tenant_id,
							"is_sleepy": is_sleepy,
							"request_number": index,
							"wait_seconds": round(time.monotonic() - started, 2),
						}
					)
					if result["is_draft"]:
						self.report["unresolved_vm"] = result["name"]
						self.save()
						raise RuntimeError(f"VM {result['name']} has an uncertain Metal create result.")
					self.save()
					break
				frappe.db.rollback()
				if index < len(sequence):
					time.sleep(self.arguments.interval_seconds)

	def cleanup(self) -> None:
		"""Delete confirmed VMs, then archive trial hosts."""
		import frappe
		from frappe.utils.background_jobs import is_job_enqueued

		from atlas.vm.core.reconciliation import reconcile_terminating
		from atlas.vm.core.vm_service import VirtualMachineService

		self.snapshot()
		unresolved = self.report.get("unresolved_vm")
		if unresolved and frappe.db.exists("Virtual Machine", unresolved):
			if frappe.db.get_value("Virtual Machine", unresolved, "is_draft"):
				raise RuntimeError(f"VM {unresolved} needs reconciliation before cleanup.")
		self.report.pop("unresolved_vm", None)
		self.save()
		deadline = time.monotonic() + self.arguments.timeout_seconds
		for name in self.report["virtual_machines"]:
			if not frappe.db.exists("Virtual Machine", name):
				continue
			vm = frappe.get_doc("Virtual Machine", name)
			if vm.is_draft:
				raise RuntimeError(f"VM {name} is still a draft. Reconcile it before cleanup.")
			if not vm.is_terminating:
				VirtualMachineService(vm).terminate()
				frappe.db.commit()
		while any(frappe.db.exists("Virtual Machine", name) for name in self.report["virtual_machines"]):
			if time.monotonic() >= deadline:
				raise TimeoutError("Timed out waiting for VM deletion. Resources remain in the report.")
			for name in self.report["virtual_machines"]:
				if frappe.db.exists("Virtual Machine", name):
					reconcile_terminating(name)
			frappe.db.commit()
			frappe.db.rollback()
			time.sleep(self.arguments.poll_seconds)
		for name in self.report["virtual_machines"]:
			if "deleted_at" not in self.report["virtual_machines"][name]:
				self.report["virtual_machines"][name]["deleted_at"] = timestamp()
				self.report["events"].append({"at": timestamp(), "kind": "vm_deleted", "name": name})
		self.save()
		for name in self.report["hosts"]:
			while True:
				if time.monotonic() >= deadline:
					raise TimeoutError("Timed out waiting for host cleanup. Resources remain in the report.")
				if not frappe.db.exists("Metal Server", name):
					self.report["hosts"][name]["status"] = "RolledBack"
					break
				server = frappe.get_doc("Metal Server", name, for_update=True)
				if frappe.db.exists("Virtual Machine", {"server": name}):
					raise RuntimeError(f"VMs still use trial host {name}. Host cleanup stopped.")
				if server.status == "Deleted":
					self.report["hosts"][name]["status"] = "Deleted"
					break
				if server.status in ("Pending", "Installing") or is_job_enqueued(server.setup_job_id):
					frappe.db.rollback()
					time.sleep(self.arguments.poll_seconds)
					continue
				server.archive_server()
				frappe.db.commit()
				self.report["hosts"][name]["status"] = "Deleted"
				self.report["events"].append({"at": timestamp(), "kind": "host_deleted", "name": name})
				self.save()
				break
			frappe.db.rollback()
		self.report["phase"] = "cleaned_up"
		self.report.pop("cleanup_error", None)
		self.report["cleaned_up_at"] = timestamp()
		self.save()


def connect_site(site: str, sites_path: Path) -> None:
	"""Connect Frappe without keeping a transaction open during sleeps."""
	import frappe

	os.chdir(sites_path)
	frappe.init(site=site, sites_path=".")
	frappe.connect()
	frappe.set_user("Administrator")


def run(arguments: argparse.Namespace) -> None:
	"""Run one strategy on a site and save its report."""
	import frappe

	if arguments.output.exists():
		raise ValueError(f"Report already exists: {arguments.output}")
	connect_site(arguments.site, arguments.sites_path)
	try:
		report = {
			"site": arguments.site,
			"strategy": arguments.strategy,
			"host_type": arguments.host_type,
			"started_at": timestamp(),
			"phase": "preflight",
			"parameters": {
				key: value
				for key, value in vars(arguments).items()
				if key not in ("command", "output", "sites_path", "apply")
			},
			"hosts": {},
			"virtual_machines": {},
			"events": [],
		}
		trial = LiveTrial(arguments, report)
		trial.preflight()
		try:
			report["phase"] = "running"
			trial.save()
			trial.create_requests()
			report["phase"] = "completed"
			report["completed_at"] = timestamp()
			trial.save()
		except (Exception, KeyboardInterrupt) as error:
			frappe.db.rollback()
			trial.snapshot()
			report["phase"] = "failed"
			report["error"] = str(error)
			trial.save()
			raise
		if not arguments.keep_resources:
			try:
				trial.cleanup()
			except (Exception, KeyboardInterrupt) as error:
				frappe.db.rollback()
				report["phase"] = "cleanup_failed"
				report["cleanup_error"] = str(error)
				trial.save()
				raise
		print(f"Trial report: {arguments.output}")
	finally:
		frappe.destroy()


def cleanup(arguments: argparse.Namespace) -> None:
	"""Resume cleanup from the report of one live trial."""
	import frappe

	report = json.loads(arguments.output.read_text())
	if report["site"] != arguments.site:
		raise ValueError("The report belongs to another site.")
	connect_site(arguments.site, arguments.sites_path)
	try:
		trial = LiveTrial(arguments, report)
		try:
			trial.cleanup()
		except (Exception, KeyboardInterrupt) as error:
			frappe.db.rollback()
			report["phase"] = "cleanup_failed"
			report["cleanup_error"] = str(error)
			trial.save()
			raise
		print(f"Cleanup report: {arguments.output}")
	finally:
		frappe.destroy()


def main() -> None:
	"""Parse arguments and run a guarded live trial or its cleanup."""
	from atlas.vm.core.placement.strategies import STRATEGIES

	parser = argparse.ArgumentParser(description=__doc__)
	commands = parser.add_subparsers(dest="command", required=True)
	run_parser = commands.add_parser("run", help="Run real VM placement on a site")
	cleanup_parser = commands.add_parser("cleanup", help="Delete resources recorded by a run")
	for command in (run_parser, cleanup_parser):
		command.add_argument("--site", required=True)
		command.add_argument("--sites-path", type=Path, default=Path("sites"))
		command.add_argument("--output", required=True, type=Path, help="JSON report path")
		command.add_argument("--poll-seconds", type=positive_integer, default=10)
		command.add_argument("--timeout-seconds", type=positive_integer, default=7200)
	run_parser.add_argument("--strategy", choices=tuple(STRATEGIES), default="balanced")
	run_parser.add_argument("--host-type", required=True, help="Metal Server Size name")
	run_parser.add_argument("--image", required=True, help="Virtual Machine Image name")
	run_parser.add_argument(
		"--tenant-id",
		dest="tenant_ids",
		type=tenant_identifier,
		action="append",
		required=True,
		metavar="ID",
		help="Repeat for each distinct tenant ID",
	)
	run_parser.add_argument("--regular-count", type=non_negative_integer, default=3)
	run_parser.add_argument("--sleepy-count", type=non_negative_integer, default=3)
	run_parser.add_argument("--cpu-millicores", type=positive_integer, default=1000)
	run_parser.add_argument("--memory-mib", type=positive_integer, default=1024)
	run_parser.add_argument("--disk-mib", type=positive_integer, default=10240)
	run_parser.add_argument("--sleep-after-idle-seconds", type=positive_integer, default=1800)
	run_parser.add_argument("--interval-seconds", type=non_negative_float, default=5)
	run_parser.add_argument("--max-hosts", type=positive_integer, default=8)
	run_parser.add_argument("--keep-resources", action="store_true")
	run_parser.add_argument("--apply", action="store_true", help="Required to create paid resources")
	arguments = parser.parse_args()
	arguments.output = arguments.output.resolve()
	arguments.sites_path = arguments.sites_path.resolve()
	if arguments.command == "run":
		if not arguments.apply:
			parser.error("run requires --apply because it creates paid provider hosts")
		if not arguments.regular_count and not arguments.sleepy_count:
			parser.error("at least one VM count must be positive")
		if len(arguments.tenant_ids) < 2 or len(set(arguments.tenant_ids)) != len(arguments.tenant_ids):
			parser.error("run requires at least two distinct --tenant-id values")
		if arguments.regular_count + arguments.sleepy_count < len(arguments.tenant_ids):
			parser.error("VM count must be at least the number of tenant IDs")
		run(arguments)
	else:
		cleanup(arguments)


if __name__ == "__main__":
	main()
