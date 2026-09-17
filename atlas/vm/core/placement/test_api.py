from datetime import datetime, timedelta
from types import SimpleNamespace
from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.api import PlacementAPI, Resources


class TestPlacementAPI(UnitTestCase):
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
			patch("atlas.vm.core.placement.api.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.api.frappe.get_all", side_effect=rows),
		):
			api = PlacementAPI(request, "amd64", 1.5)

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
			patch("atlas.vm.core.placement.api.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.api.frappe.get_all", side_effect=rows),
		):
			api = PlacementAPI(VirtualMachineCreateRequest("image", 1000, 1024, 10240, 7), "amd64", 1.0)
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
			patch("atlas.vm.core.placement.api.now_datetime", return_value=now),
			patch("atlas.vm.core.placement.api.frappe.get_all", side_effect=rows),
			patch("atlas.vm.core.placement.api.frappe.get_doc", return_value=locked) as get_doc,
		):
			api = PlacementAPI(VirtualMachineCreateRequest("image", 32000, 1024, 10240, 7), "amd64", 1.0)
			self.assertFalse(api.select("a"))

		get_doc.assert_called_once_with("Metal Server", "a", for_update=True)
		self.assertIsNone(api._selected_server)

	def test_stale_samples_report_a_sync_fault(self) -> None:
		def rows(doctype: str, **kwargs: object) -> list[SimpleNamespace]:
			if doctype == "Metal Server":
				return [SimpleNamespace(name="a", architecture="amd64", is_sleepy=0)]
			return []

		with patch("atlas.vm.core.placement.api.frappe.get_all", side_effect=rows):
			with self.assertRaisesRegex(frappe.ValidationError, "capacity sample"):
				PlacementAPI(VirtualMachineCreateRequest("image", 1000, 1024, 10240, 7), "amd64", 1.0)
