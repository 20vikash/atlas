"""Checks for the live placement trial's workload and provider host limit."""

import sys
from argparse import Namespace
from datetime import datetime
from types import SimpleNamespace
from unittest import TestCase
from unittest.mock import Mock, patch

import frappe

from atlas.metal_server.doctype.metal_server.metal_server import MetalServer
from atlas.vm.core.placement.models import PlacementDemand
from atlas.vm.core.placement.simulation import live
from atlas.vm.core.placement.simulation.live import (
	HostLimitReached,
	LiveTrial,
	request_sequence,
	trial_placement_context,
)
from atlas.vm.core.placement.strategies.best_fit import BestFitStrategy
from atlas.vm.core.vm_service import VirtualMachineService


class TestLiveTrial(TestCase):
	def test_cli_accepts_distinct_tenant_ids(self) -> None:
		arguments = [
			"live",
			"run",
			"--site",
			"trial.localhost",
			"--output",
			"/tmp/trial.json",
			"--strategy",
			"spread-3",
			"--host-type",
			"size",
			"--image",
			"image",
			"--tenant-id",
			"7",
			"--tenant-id",
			"8",
			"--apply",
		]
		with patch.object(sys, "argv", arguments), patch.object(live, "run") as run:
			live.main()
		self.assertEqual(run.call_args.args[0].tenant_ids, [7, 8])

	def test_cli_uses_balanced_when_strategy_is_omitted(self) -> None:
		arguments = [
			"live",
			"run",
			"--site",
			"trial.localhost",
			"--output",
			"/tmp/trial.json",
			"--host-type",
			"size",
			"--image",
			"image",
			"--tenant-id",
			"7",
			"--tenant-id",
			"8",
			"--apply",
		]
		with patch.object(sys, "argv", arguments), patch.object(live, "run") as run:
			live.main()
		self.assertEqual(run.call_args.args[0].strategy, "balanced")

	def test_request_sequence_interleaves_both_pools(self) -> None:
		self.assertEqual(
			request_sequence(3, 2, [11, 22]),
			[(False, 11), (True, 22), (False, 22), (True, 11), (False, 11)],
		)
		self.assertEqual(request_sequence(1, 1, [11, 22]), [(False, 11), (True, 22)])

	def test_host_limit_blocks_the_provider_request(self) -> None:
		report = {"hosts": {"new1": {}, "new2": {}}, "events": []}
		trial = LiveTrial(Namespace(max_hosts=3, output=None), report)
		provider = Mock(
			return_value=SimpleNamespace(
				name="new3", status="Pending", is_sleepy=True, is_provisioning_completed=False
			)
		)
		with patch.object(
			frappe,
			"get_all",
			side_effect=[["old", "new1", "new2"], ["old", "new1", "new2", "new3"]],
		):
			with patch.object(MetalServer, "provision", provider):
				with patch.object(trial, "save"):
					provision = trial.guarded_provision()
					provision(size="test", is_sleepy=True)
					with self.assertRaises(HostLimitReached):
						provision(size="test", is_sleepy=True)
		provider.assert_called_once_with(size="test", is_sleepy=True)
		self.assertIn("new3", report["hosts"])

	def test_best_fit_uses_preexisting_ready_host_without_provisioning(self) -> None:
		api_class = trial_placement_context("size")
		api = api_class.__new__(api_class)
		preexisting = SimpleNamespace(name="old", architecture="amd64", is_sleepy=False)
		resources = SimpleNamespace(cpu_millicores=10000, memory_mib=10000, storage_mib=10000)
		host_usage = SimpleNamespace(
			name="old",
			architecture="amd64",
			is_sleepy=False,
			total=resources,
			free=resources,
			tenant_vm_count=0,
			sleepy_reserved_memory_mib=0,
		)
		with (
			patch.object(frappe, "get_all", return_value=[preexisting]),
			patch.object(api, "_latest_samples", return_value={"old": object()}) as latest_samples,
			patch.object(api, "_virtual_machines", return_value={"old": []}),
			patch.object(api, "_migration_reservations", return_value={"old": []}),
			patch.object(api, "_host_usage", return_value=host_usage),
		):
			usage = api._load_usage()
		self.assertEqual(usage.hosts, (host_usage,))
		self.assertEqual(api._ready_server_names, frozenset({"old"}))
		latest_samples.assert_called_once_with(["old"])

		api.request = PlacementDemand(1000, 1000, 1000, "amd64", 7, False)
		api.action = "create"
		api.current_host_name = None
		api.usage = usage
		api.select = Mock(return_value=True)
		api.spawn_host = Mock()

		BestFitStrategy().select_host(api)

		api.select.assert_called_once_with("old")
		api.spawn_host.assert_not_called()

	def test_trial_uses_selected_host_type_for_both_pools(self) -> None:
		api_class = trial_placement_context("size")
		api = api_class.__new__(api_class)
		api._host_intents = []

		api.spawn_host()
		api.spawn_host(is_sleepy=True)

		self.assertEqual(api._host_intents, [("size", 1, False), ("size", 1, True)])

	def test_refresh_capacity_queues_stale_hosts_without_blocking_fresh_capacity(self) -> None:
		trial = LiveTrial(Namespace(output=None), {"hosts": {"owned": {}}})
		database = SimpleNamespace(exists=Mock(side_effect=[False, True]))
		with (
			patch.object(frappe, "get_all", return_value=["old", "owned"]),
			patch.object(frappe, "db", database),
			patch("frappe.utils.now_datetime", return_value=datetime(2026, 9, 18)),
			patch("atlas.metal_server.usage.enqueue_server_sync") as enqueue_server_sync,
		):
			self.assertFalse(trial.refresh_capacity())
		enqueue_server_sync.assert_called_once_with("old")

	def test_refresh_capacity_waits_when_every_ready_host_is_stale(self) -> None:
		trial = LiveTrial(Namespace(output=None), {"hosts": {}})
		with (
			patch.object(frappe, "get_all", return_value=["old"]),
			patch.object(frappe, "db", SimpleNamespace(exists=Mock(return_value=False))),
			patch("frappe.utils.now_datetime", return_value=datetime(2026, 9, 18)),
			patch("atlas.metal_server.usage.enqueue_server_sync") as enqueue_server_sync,
		):
			self.assertTrue(trial.refresh_capacity())
		enqueue_server_sync.assert_called_once_with("old")

	def test_snapshot_records_only_trial_resources(self) -> None:
		host = SimpleNamespace(
			name="owned", status="Running", is_sleepy=False, is_provisioning_completed=True
		)
		preexisting_host = SimpleNamespace(
			name="old", status="Running", is_sleepy=False, is_provisioning_completed=True
		)
		vm = SimpleNamespace(name="owned-vm", server="owned", is_draft=False, is_terminating=False)
		preexisting_vm = SimpleNamespace(name="old-vm", server="old", is_draft=False, is_terminating=False)
		report = {
			"hosts": {"owned": {"status": "Pending"}},
			"virtual_machines": {"owned-vm": {"created_at": "earlier"}},
			"events": [],
		}
		trial = LiveTrial(Namespace(output=None), report)
		with (
			patch.object(frappe, "get_all", side_effect=[[host, preexisting_host], [vm, preexisting_vm]]),
			patch.object(trial, "save"),
		):
			trial.snapshot()
		self.assertEqual(set(report["hosts"]), {"owned"})
		self.assertEqual(set(report["virtual_machines"]), {"owned-vm"})
		self.assertEqual(report["virtual_machines"]["owned-vm"]["server"], "owned")

	def test_uncertain_vm_create_stops_without_retry(self) -> None:
		arguments = Namespace(
			output=None,
			host_type="size",
			strategy="spread-3",
			timeout_seconds=60,
			regular_count=1,
			sleepy_count=1,
			tenant_ids=[7, 8],
			interval_seconds=0,
		)
		report = {"events": [], "hosts": {}, "virtual_machines": {}}
		trial = LiveTrial(arguments, report)
		with (
			patch.object(frappe, "db", SimpleNamespace(commit=Mock(), rollback=Mock())),
			patch.object(trial, "refresh_capacity", return_value=False),
			patch.object(trial, "build_request", return_value=object()),
			patch.object(trial, "guarded_provision", return_value=Mock()),
			patch.object(trial, "snapshot"),
			patch.object(trial, "save"),
			patch.object(
				VirtualMachineService, "create", return_value={"name": "vm-1", "is_draft": True}
			) as create,
			self.assertRaisesRegex(RuntimeError, "uncertain Metal create"),
		):
			trial.create_requests()
		create.assert_called_once()
		self.assertEqual(report["unresolved_vm"], "vm-1")
