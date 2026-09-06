from datetime import datetime
from types import SimpleNamespace
from unittest.mock import Mock, call, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement import PlacementCapacity, PlacementService


class TestPlacementCapacity(UnitTestCase):
	def test_reserve_subtracts_each_resource(self) -> None:
		capacity = PlacementCapacity("server-1", "amd64", datetime.now(), 8, 16384, 102400)

		remaining = capacity.reserve(2, 2048, 10240)

		self.assertEqual(remaining.available_cpu_count, 6)
		self.assertEqual(remaining.available_memory_mib, 14336)
		self.assertEqual(remaining.available_storage_mib, 92160)

	def test_capacity_checks_each_resource_and_architecture(self) -> None:
		request = VirtualMachineCreateRequest("image", 2, 2048, 10240, 7)
		capacity = PlacementCapacity("server-1", "amd64", datetime.now(), 4, 4096, 20480)

		self.assertTrue(capacity.can_host(request, "amd64"))
		self.assertFalse(capacity.can_host(request, "arm64"))


class TestPlacementService(UnitTestCase):
	def test_selection_reports_a_missing_current_capacity_sample(self) -> None:
		service = PlacementService()
		service.get_ready_servers = Mock(
			return_value=[SimpleNamespace(name="server-1", architecture="amd64")]
		)
		service.get_latest_capacities = Mock(return_value={})

		with self.assertRaisesRegex(frappe.ValidationError, "capacity sample"):
			service.select_server(VirtualMachineCreateRequest("image", 2, 2048, 10240, 7), "amd64")

	def test_latest_capacities_use_only_fresh_samples(self) -> None:
		current_time = datetime(2026, 9, 6, 8, 0)
		latest_sample = SimpleNamespace(
			server="server-1",
			creation=datetime(2026, 9, 6, 7, 59),
			available_cpu_count=4,
			available_memory_mib=4096,
			available_storage_mib=20480,
		)
		older_sample = SimpleNamespace(
			server="server-1",
			creation=datetime(2026, 9, 6, 7, 58, 30),
			available_cpu_count=8,
			available_memory_mib=8192,
			available_storage_mib=40960,
		)

		with (
			patch("atlas.vm.core.placement.now_datetime", return_value=current_time),
			patch(
				"atlas.vm.core.placement.frappe.get_all",
				return_value=[latest_sample, older_sample],
			) as get_all,
		):
			capacities = PlacementService().get_latest_capacities({"server-1": "amd64"})

		self.assertEqual(capacities["server-1"].available_cpu_count, 4)
		self.assertEqual(
			get_all.call_args.kwargs["filters"]["creation"],
			[">=", datetime(2026, 9, 6, 7, 58)],
		)

	def test_local_reservations_include_uncertain_drafts(self) -> None:
		created_at = datetime.now()
		capacity = PlacementCapacity("server-1", "amd64", created_at, 8, 16384, 102400)
		reservations = [
			SimpleNamespace(vcpus=2, memory_mib=2048, disk_mib=10240, is_draft=1),
			SimpleNamespace(vcpus=1, memory_mib=1024, disk_mib=5120, is_draft=0),
		]

		with patch("atlas.vm.core.placement.frappe.get_all", return_value=reservations) as get_all:
			remaining = PlacementService().subtract_local_reservations(capacity)

		self.assertEqual(remaining.available_cpu_count, 5)
		self.assertEqual(remaining.available_memory_mib, 13312)
		self.assertEqual(remaining.available_storage_mib, 87040)
		get_all.assert_called_once_with(
			"Virtual Machine",
			filters={"server": "server-1"},
			or_filters={"creation": [">", created_at], "is_draft": 1},
			fields=["vcpus", "memory_mib", "disk_mib"],
		)

	def test_selection_locks_and_rechecks_the_candidate(self) -> None:
		request = VirtualMachineCreateRequest("image", 2, 2048, 10240, 7)
		created_at = datetime.now()
		capacity = PlacementCapacity("server-1", "amd64", created_at, 4, 4096, 20480)
		locked_server = SimpleNamespace(
			name="server-1",
			architecture="amd64",
			status="Running",
			is_provisioning_completed=1,
		)
		service = PlacementService()
		service.get_ready_servers = Mock(
			return_value=[SimpleNamespace(name="server-1", architecture="amd64")]
		)
		service.get_latest_capacities = Mock(side_effect=[{"server-1": capacity}, {"server-1": capacity}])
		service.subtract_local_reservations = Mock(side_effect=[capacity, capacity.reserve(1, 1024, 5120)])
		service.lock_server = Mock(return_value=locked_server)

		selected_server = service.select_server(request, "amd64")

		self.assertIs(selected_server, locked_server)
		service.lock_server.assert_called_once_with("server-1")
		self.assertEqual(
			service.get_latest_capacities.call_args_list,
			[call({"server-1": "amd64"}), call({"server-1": "amd64"})],
		)

	def test_selection_rejects_capacity_consumed_while_waiting_for_the_lock(self) -> None:
		request = VirtualMachineCreateRequest("image", 2, 2048, 10240, 7)
		capacity = PlacementCapacity("server-1", "amd64", datetime.now(), 2, 2048, 10240)
		locked_server = SimpleNamespace(
			name="server-1",
			architecture="amd64",
			status="Running",
			is_provisioning_completed=1,
		)
		service = PlacementService()
		service.get_ready_servers = Mock(
			return_value=[SimpleNamespace(name="server-1", architecture="amd64")]
		)
		service.get_latest_capacities = Mock(side_effect=[{"server-1": capacity}, {"server-1": capacity}])
		service.subtract_local_reservations = Mock(side_effect=[capacity, capacity.reserve(1, 1, 1)])
		service.lock_server = Mock(return_value=locked_server)

		with self.assertRaises(frappe.ValidationError):
			service.select_server(request, "amd64")

		service.lock_server.assert_called_once_with("server-1")
