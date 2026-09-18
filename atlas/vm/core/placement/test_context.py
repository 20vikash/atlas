from datetime import datetime, timedelta
from types import SimpleNamespace
from unittest.mock import call, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.context import CapacityPending, PlacementContext, PlacementDemand, Resources


class TestPlacementContext(UnitTestCase):
	@staticmethod
	def _spawn_api() -> PlacementContext:
		api = PlacementContext.__new__(PlacementContext)
		api.request = PlacementDemand(2000, 2048, 10240, "amd64", 7, False)
		api._stale_ready_servers = {}
		api._pending_hosts = set()
		api._host_intents = []
		api._selected_server = None
		return api

	@staticmethod
	def _catalog(
		host_type: str = "Scaleway/size",
	) -> tuple[SimpleNamespace, SimpleNamespace, SimpleNamespace]:
		settings = SimpleNamespace(new_host_type="Scaleway/size", server_provider="Scaleway")
		size = SimpleNamespace(
			doctype="Metal Server Size",
			name=host_type,
			enabled=1,
			provider_type="Scaleway",
			architecture="amd64",
			cpu_count=8,
			memory_mib=32768,
			disk_gib=100,
			provider_metadata='{"hourly": {"id": "offer"}}',
		)
		image = SimpleNamespace(
			doctype="Metal Server Image",
			name="Scaleway/Ubuntu_26.04",
			enabled=1,
			provider_type="Scaleway",
			provider_metadata='{"id": "image"}',
		)
		return settings, size, image

	def test_spawn_uses_configured_host_type_and_strategy_override(self) -> None:
		for host_type in (None, "Scaleway/override"):
			api = self._spawn_api()
			settings, size, image = self._catalog(host_type or "Scaleway/size")
			created = SimpleNamespace(name="node-new")
			with (
				patch(
					"atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image]
				) as get_doc,
				patch("atlas.vm.core.placement.context.frappe.db.sql", return_value=[]) as sql,
				patch(
					"atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision",
					return_value=created,
				) as provision,
			):
				self.assertEqual(api._ensure_pending_hosts(host_type, 1, False), ("node-new",))

			get_doc.assert_has_calls(
				[call("Atlas Settings", for_update=True), call("Metal Server Size", size.name)]
			)
			self.assertEqual(sql.call_args.args[1], {"server_size": size.name})
			self.assertIn("for update", sql.call_args.args[0])
			provision.assert_called_once_with(size=size.name, is_sleepy=False)

	def test_spawn_reuses_pending_hosts_without_creating_another(self) -> None:
		api = self._spawn_api()
		settings, size, image = self._catalog()
		pending = [
			SimpleNamespace(name="node-a", status="Pending", architecture="amd64", is_sleepy=0),
			SimpleNamespace(name="node-b", status="Installing", architecture="amd64", is_sleepy=0),
		]
		with (
			patch("atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image] * 2),
			patch("atlas.vm.core.placement.context.frappe.db.sql", return_value=pending),
			patch("atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision") as provision,
		):
			self.assertEqual(api._ensure_pending_hosts(None, 1, False), ("node-a", "node-b"))
			self.assertEqual(api._ensure_pending_hosts(None, 1, False), ("node-a", "node-b"))

		provision.assert_not_called()

	def test_spawn_separates_pending_sleepy_and_regular_hosts(self) -> None:
		api = self._spawn_api()
		api.request = PlacementDemand(2000, 2048, 10240, "amd64", 7, True)
		settings, size, image = self._catalog()
		regular = SimpleNamespace(name="regular", status="Pending", architecture="amd64", is_sleepy=0)
		sleepy = SimpleNamespace(name="sleepy", status="Pending", architecture="amd64", is_sleepy=1)
		with (
			patch("atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image] * 2),
			patch(
				"atlas.vm.core.placement.context.frappe.db.sql", side_effect=[[regular], [sleepy, regular]]
			),
			patch(
				"atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision",
				return_value=SimpleNamespace(name="sleepy"),
			) as provision,
		):
			self.assertEqual(api._ensure_pending_hosts(None, 1, True), ("sleepy",))
			self.assertEqual(api._ensure_pending_hosts(None, 1, True), ("sleepy",))

		provision.assert_called_once_with(size=size.name, is_sleepy=True)

	def test_explicit_count_creates_only_missing_hosts(self) -> None:
		api = self._spawn_api()
		settings, size, image = self._catalog()
		pending = [SimpleNamespace(name="node-a", status="Pending", architecture="amd64", is_sleepy=0)]
		with (
			patch("atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image] * 2),
			patch(
				"atlas.vm.core.placement.context.frappe.db.sql",
				side_effect=[
					pending,
					[
						*pending,
						SimpleNamespace(name="node-b", status="Pending", architecture="amd64", is_sleepy=0),
						SimpleNamespace(name="node-c", status="Pending", architecture="amd64", is_sleepy=0),
					],
				],
			),
			patch(
				"atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision",
				side_effect=[SimpleNamespace(name="node-b"), SimpleNamespace(name="node-c")],
			) as provision,
		):
			self.assertEqual(api._ensure_pending_hosts(None, 3, False), ("node-a", "node-b", "node-c"))
			self.assertEqual(api._ensure_pending_hosts(None, 3, False), ("node-a", "node-b", "node-c"))

		self.assertEqual(provision.call_count, 2)

	def test_spawn_reports_invalid_catalog_before_creating_a_host(self) -> None:
		for field, value in (("architecture", "arm64"), ("memory_mib", 1024), ("provider_metadata", "{}")):
			api = self._spawn_api()
			settings, size, image = self._catalog()
			setattr(size, field, value)
			with (
				patch("atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image]),
				patch("atlas.vm.core.placement.context.frappe.db.sql") as sql,
				self.assertRaises(frappe.ValidationError),
			):
				api._ensure_pending_hosts(None, 1, False)

			sql.assert_not_called()

	def test_spawn_reports_latest_failed_provisioning(self) -> None:
		api = self._spawn_api()
		settings, size, image = self._catalog()
		with (
			patch("atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image]),
			patch(
				"atlas.vm.core.placement.context.frappe.db.sql",
				return_value=[
					SimpleNamespace(name="node-failed", status="Failed", architecture="amd64", is_sleepy=0)
				],
			),
			patch("atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision") as provision,
			self.assertRaisesRegex(frappe.ValidationError, "node-failed"),
		):
			api._ensure_pending_hosts(None, 1, False)

		provision.assert_not_called()

	def test_failed_regular_host_does_not_block_sleepy_provisioning(self) -> None:
		api = self._spawn_api()
		settings, size, image = self._catalog()
		failed = SimpleNamespace(name="regular-failed", status="Failed", architecture="amd64", is_sleepy=0)
		with (
			patch("atlas.vm.core.placement.context.frappe.get_doc", side_effect=[settings, size, image]),
			patch("atlas.vm.core.placement.context.frappe.db.sql", return_value=[failed]),
			patch(
				"atlas.metal_server.doctype.metal_server.metal_server.MetalServer.provision",
				return_value=SimpleNamespace(name="sleepy-new"),
			) as provision,
		):
			self.assertEqual(api._ensure_pending_hosts(None, 1, True), ("sleepy-new",))

		provision.assert_called_once_with(size=size.name, is_sleepy=True)

	def test_pending_result_commits_host_intent_without_a_vm_draft(self) -> None:
		api = self._spawn_api()
		api.spawn_host()
		order = []
		with (
			patch(
				"atlas.vm.core.placement.context.frappe.db.rollback",
				side_effect=lambda: order.append("rollback"),
			),
			patch.object(
				api,
				"_ensure_pending_hosts",
				side_effect=lambda *args: order.append("ensure") or ("node-new",),
			),
			patch(
				"atlas.vm.core.placement.context.frappe.db.commit", side_effect=lambda: order.append("commit")
			),
			self.assertRaisesRegex(CapacityPending, "node-new"),
		):
			api.finish()

		self.assertEqual(order, ["rollback", "ensure", "commit"])

	def test_selected_result_keeps_host_intent_in_caller_transaction(self) -> None:
		api = self._spawn_api()
		server = SimpleNamespace(name="ready")
		api._selected_server = server
		api.spawn_host()
		with (
			patch.object(api, "_ensure_pending_hosts", return_value=("node-new",)) as ensure,
			patch("atlas.vm.core.placement.context.frappe.db.rollback") as rollback,
			patch("atlas.vm.core.placement.context.frappe.db.commit") as commit,
		):
			self.assertIs(api.finish(), server)

		ensure.assert_called_once_with(None, 1, False)
		rollback.assert_not_called()
		commit.assert_not_called()

	def test_stale_sample_does_not_trigger_host_creation(self) -> None:
		api = self._spawn_api()
		api._stale_ready_servers = {"node-stale": ("amd64", False)}
		with (
			patch("atlas.vm.core.placement.context.frappe.get_doc") as get_doc,
			self.assertRaisesRegex(frappe.ValidationError, "node-stale"),
		):
			api._ensure_pending_hosts(None, 1, False)

		get_doc.assert_not_called()

	def test_empty_fleet_can_request_its_first_host(self) -> None:
		with patch("atlas.vm.core.placement.context.frappe.get_all", return_value=[]):
			api = PlacementContext(VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7), "amd64", 1.0)

		self.assertEqual(api.usage.hosts, ())
		self.assertEqual(api.usage.total, Resources(0, 0, 0))

	def test_usage_aggregates_hosts_and_local_reservations(self) -> None:
		now = datetime(2026, 9, 17, 12)
		sample_time = now - timedelta(seconds=30)
		servers = [
			SimpleNamespace(name="a", architecture="amd64", is_sleepy=1),
			SimpleNamespace(name="b", architecture="amd64", is_sleepy=0),
		]
		samples = [
			SimpleNamespace(
				server="a",
				creation=sample_time,
				total_cpu_millicores=8000,
				available_cpu_millicores=6000,
				total_memory_mib=16384,
				available_memory_mib=10000,
				total_storage_mib=102400,
				available_storage_mib=80000,
			),
			SimpleNamespace(
				server="b",
				creation=sample_time,
				total_cpu_millicores=4000,
				available_cpu_millicores=3000,
				total_memory_mib=8192,
				available_memory_mib=6000,
				total_storage_mib=50000,
				available_storage_mib=40000,
			),
		]
		virtual_machines = [
			SimpleNamespace(
				server="a",
				tenant_id=7,
				sleep_after_idle_seconds=60,
				cpu_millicores=1000,
				memory_mib=2048,
				disk_mib=5000,
				creation=sample_time - timedelta(seconds=1),
				is_draft=0,
			),
			SimpleNamespace(
				server="a",
				tenant_id=7,
				sleep_after_idle_seconds=0,
				cpu_millicores=1000,
				memory_mib=1024,
				disk_mib=10000,
				creation=sample_time - timedelta(seconds=1),
				is_draft=1,
			),
			SimpleNamespace(
				server="b",
				tenant_id=8,
				sleep_after_idle_seconds=60,
				cpu_millicores=500,
				memory_mib=2048,
				disk_mib=5000,
				creation=sample_time + timedelta(seconds=1),
				is_draft=0,
			),
		]

		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return servers
			if doctype == "Metal Server Usage":
				return samples
			if doctype == "Virtual Machine Migration":
				return [SimpleNamespace(target_server="b", virtual_machine="incoming")]
			if doctype == "Virtual Machine" and "name" in kwargs["filters"]:
				return [SimpleNamespace(name="incoming", cpu_millicores=200, memory_mib=512, disk_mib=1000)]
			return virtual_machines

		request = VirtualMachineCreateRequest("image", 32000, 2048, 10240, 7, sleep_after_idle_seconds=60)
		with (
			patch("atlas.vm.core.placement.context.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows),
		):
			api = PlacementContext(request, "amd64", 1.5)

		self.assertTrue(api.request.is_sleepy)
		self.assertEqual(api.request.tenant_id, 7)
		self.assertEqual(api.sleepy_vm_overcommit_factor, 1.5)
		self.assertEqual(api.usage.total, Resources(12000, 24576, 152400))
		self.assertEqual(api.usage.free, Resources(7300, 12416, 104000))
		self.assertEqual(api.usage.tenant_vm_count, 2)
		self.assertEqual(api.usage.sleepy_reserved_memory_mib, 4096)
		self.assertEqual(api.usage.hosts[0].free, Resources(5000, 8976, 70000))
		self.assertTrue(api.usage.hosts[0].is_sleepy)
		self.assertEqual(api.usage.hosts[1].free, Resources(2300, 3440, 34000))

	def test_rate_counts_recent_placements_including_drafts(self) -> None:
		now = datetime(2026, 9, 17, 12)
		sample = SimpleNamespace(
			server="a",
			creation=now,
			total_cpu_millicores=1000,
			available_cpu_millicores=1000,
			total_memory_mib=4096,
			available_memory_mib=4096,
			total_storage_mib=20480,
			available_storage_mib=20480,
		)
		rate_filters: list[object] = []

		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return [SimpleNamespace(name="a", architecture="amd64", is_sleepy=0)]
			if doctype == "Metal Server Usage":
				return [sample]
			if doctype == "Virtual Machine" and "creation" in kwargs["filters"]:
				rate_filters.append(kwargs["filters"])
				return [SimpleNamespace(server="a"), SimpleNamespace(server="a"), SimpleNamespace(server="b")]
			return []

		with (
			patch("atlas.vm.core.placement.context.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows),
		):
			api = PlacementContext(VirtualMachineCreateRequest("image", 1000, 1024, 10240, 7), "amd64", 1.0)
			self.assertEqual(api.placement_rate(), 0.6)
			self.assertEqual(api.placement_rate("a"), 0.4)
			self.assertEqual(api.placement_rate("b"), 0.2)

		self.assertEqual(rate_filters, [{"creation": [">=", now - timedelta(minutes=5)]}])

	def test_select_rechecks_capacity_after_lock(self) -> None:
		now = datetime(2026, 9, 17, 12)
		sample = SimpleNamespace(
			server="a",
			creation=now,
			total_cpu_millicores=1000,
			available_cpu_millicores=1000,
			total_memory_mib=4096,
			available_memory_mib=4096,
			total_storage_mib=20480,
			available_storage_mib=20480,
		)
		locked = SimpleNamespace(
			name="a", architecture="amd64", is_sleepy=0, status="Running", is_provisioning_completed=1
		)
		virtual_machine_reads = 0

		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			nonlocal virtual_machine_reads
			if doctype == "Metal Server":
				return [SimpleNamespace(name="a", architecture="amd64", is_sleepy=0)]
			if doctype == "Metal Server Usage":
				return [sample]
			if doctype == "Virtual Machine":
				virtual_machine_reads += 1
				if virtual_machine_reads == 2:
					return [
						SimpleNamespace(
							server="a",
							tenant_id=7,
							sleep_after_idle_seconds=0,
							cpu_millicores=1000,
							memory_mib=3500,
							disk_mib=10240,
							creation=now,
							is_draft=1,
						)
					]
			return []

		with (
			patch("atlas.vm.core.placement.context.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows),
			patch("atlas.vm.core.placement.context.frappe.get_doc", return_value=locked) as get_doc,
		):
			api = PlacementContext(VirtualMachineCreateRequest("image", 32000, 1024, 10240, 7), "amd64", 1.0)
			self.assertFalse(api.select("a"))

		get_doc.assert_called_once_with("Metal Server", "a", for_update=True)
		self.assertIsNone(api._selected_server)

	def test_select_accepts_memory_and_storage_fit_with_oversubscribed_cpu(self) -> None:
		now = datetime(2026, 9, 17, 12)
		sample = SimpleNamespace(
			server="a",
			creation=now,
			total_cpu_millicores=1000,
			available_cpu_millicores=0,
			total_memory_mib=4096,
			available_memory_mib=4096,
			total_storage_mib=20480,
			available_storage_mib=20480,
		)
		locked = SimpleNamespace(
			name="a", architecture="amd64", is_sleepy=0, status="Running", is_provisioning_completed=1
		)

		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return [SimpleNamespace(name="a", architecture="amd64", is_sleepy=0)]
			if doctype == "Metal Server Usage":
				return [sample]
			return []

		with (
			patch("atlas.vm.core.placement.context.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows),
			patch("atlas.vm.core.placement.context.frappe.get_doc", return_value=locked),
		):
			api = PlacementContext(VirtualMachineCreateRequest("image", 32000, 1024, 10240, 7), "amd64", 1.0)
			self.assertTrue(api.select("a"))
			self.assertIs(api._selected_server, locked)

	def test_stale_samples_report_a_sync_fault(self) -> None:
		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return [SimpleNamespace(name="a", architecture="amd64", is_sleepy=0)]
			return []

		with patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows):
			with self.assertRaisesRegex(frappe.ValidationError, "capacity sample"):
				PlacementContext(VirtualMachineCreateRequest("image", 1000, 1024, 10240, 7), "amd64", 1.0)

	def test_a_target_with_a_stale_sample_reports_a_sync_fault(self) -> None:
		now = datetime(2026, 9, 17, 12)
		sample = SimpleNamespace(
			server="fresh",
			creation=now,
			total_cpu_millicores=1000,
			available_cpu_millicores=1000,
			total_memory_mib=4096,
			available_memory_mib=4096,
			total_storage_mib=20480,
			available_storage_mib=20480,
		)

		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return [
					SimpleNamespace(name="fresh", architecture="amd64", is_sleepy=0),
					SimpleNamespace(name="stale", architecture="amd64", is_sleepy=0),
				]
			if doctype == "Metal Server Usage":
				return [sample]
			return []

		with (
			patch("atlas.vm.core.placement.context.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows),
		):
			api = PlacementContext(VirtualMachineCreateRequest("image", 1000, 1024, 10240, 7), "amd64", 1.0)
			with self.assertRaisesRegex(frappe.ValidationError, "Metal Server stale"):
				api.select("stale")

	def test_select_retries_when_the_sleepy_host_flag_changes(self) -> None:
		now = datetime(2026, 9, 17, 12)
		sample = SimpleNamespace(
			server="a",
			creation=now,
			total_cpu_millicores=1000,
			available_cpu_millicores=1000,
			total_memory_mib=4096,
			available_memory_mib=4096,
			total_storage_mib=20480,
			available_storage_mib=20480,
		)

		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return [SimpleNamespace(name="a", architecture="amd64", is_sleepy=0)]
			if doctype == "Metal Server Usage":
				return [sample]
			return []

		locked = SimpleNamespace(
			name="a", architecture="amd64", is_sleepy=1, status="Running", is_provisioning_completed=1
		)
		with (
			patch("atlas.vm.core.placement.context.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.context.frappe.get_all", side_effect=rows),
			patch("atlas.vm.core.placement.context.frappe.get_doc", return_value=locked),
		):
			api = PlacementContext(VirtualMachineCreateRequest("image", 1000, 1024, 10240, 7), "amd64", 1.0)
			self.assertFalse(api.select("a"))
