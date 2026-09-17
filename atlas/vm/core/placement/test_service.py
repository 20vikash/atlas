from datetime import datetime
from types import SimpleNamespace
from unittest.mock import Mock, call, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.api import HostUsage, PlacementRequest, Resources
from atlas.vm.core.placement.service import PlacementService
from atlas.vm.core.placement.strategies.default import select_host


class TestPlacementService(UnitTestCase):
	def test_configured_strategy_receives_the_api(self) -> None:
		request = VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7)
		server = SimpleNamespace(name="metal-1")
		api = SimpleNamespace(_selected_server=server)
		strategy = Mock()

		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Custom", sleepy_vm_overcommit_factor=1.5),
			),
			patch("atlas.vm.core.placement.service.STRATEGIES", {"Custom": strategy}),
			patch("atlas.vm.core.placement.service.PlacementAPI", return_value=api) as placement_api,
		):
			selected = PlacementService().select_server(request, "amd64", {"source"})

		self.assertIs(selected, server)
		placement_api.assert_called_once_with(request, "amd64", 1.5, {"source"})
		strategy.assert_called_once_with(api)

	def test_strategy_without_a_selection_reports_no_capacity(self) -> None:
		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Default", sleepy_vm_overcommit_factor=1.0),
			),
			patch(
				"atlas.vm.core.placement.service.PlacementAPI",
				return_value=SimpleNamespace(_selected_server=None),
			),
			patch("atlas.vm.core.placement.service.STRATEGIES", {"Default": Mock()}),
			self.assertRaisesRegex(AtlasUserError, "current capacity"),
		):
			PlacementService().select_server(
				VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7), "amd64"
			)

	def test_unknown_configured_strategy_is_rejected(self) -> None:
		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Missing"),
			),
			self.assertRaisesRegex(frappe.ValidationError, "Unknown placement strategy"),
		):
			PlacementService().select_server(
				VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7), "amd64"
			)


class TestDefaultStrategy(UnitTestCase):
	@staticmethod
	def _host(
		name: str,
		*,
		architecture: str = "amd64",
		is_sleepy: bool = False,
		free_cpu_millicores: int = 4000,
		free_memory_mib: int = 4096,
		free_storage_mib: int = 40000,
		tenant_vm_count: int = 0,
		sleepy_reserved_memory_mib: int = 0,
	) -> HostUsage:
		return HostUsage(
			name=name,
			architecture=architecture,
			is_sleepy=is_sleepy,
			sample_created_at=datetime(2026, 9, 17, 12),
			total=Resources(8000, 8192, 50000),
			free=Resources(free_cpu_millicores, free_memory_mib, free_storage_mib),
			tenant_vm_count=tenant_vm_count,
			sleepy_reserved_memory_mib=sleepy_reserved_memory_mib,
		)

	@staticmethod
	def _api(
		hosts: tuple[HostUsage, ...],
		*,
		is_sleepy: bool = False,
		factor: float = 1.0,
		rates: dict[str, float] | None = None,
	) -> SimpleNamespace:
		rates = rates or {}
		return SimpleNamespace(
			usage=SimpleNamespace(hosts=hosts),
			request=PlacementRequest(2000, 1024, 1000, "amd64", 7, is_sleepy),
			sleepy_vm_overcommit_factor=factor,
			placement_rate=Mock(side_effect=lambda host_name: rates.get(host_name, 0.0)),
			select=Mock(return_value=True),
		)

	def test_prefers_sleepy_host_and_retries_after_a_miss(self) -> None:
		api = self._api(
			(
				self._host("awake"),
				self._host("sleepy", is_sleepy=True),
				self._host("wrong-architecture", architecture="arm64"),
				self._host("no-memory", free_memory_mib=512),
				self._host("no-storage", free_storage_mib=512),
			),
			is_sleepy=True,
		)
		api.select.side_effect = [False, True]

		select_host(api)

		self.assertEqual(api.select.call_args_list, [call("sleepy"), call("awake")])

	def test_spreads_tenant_vms_before_considering_rate(self) -> None:
		api = self._api(
			(self._host("same-tenant", tenant_vm_count=1), self._host("other-tenant")),
			rates={"same-tenant": 0.0, "other-tenant": 1.0},
		)

		select_host(api)

		api.select.assert_called_once_with("other-tenant")

	def test_prefers_a_lower_recent_placement_rate(self) -> None:
		api = self._api(
			(self._host("busy"), self._host("quiet")),
			rates={"busy": 0.8, "quiet": 0.2},
		)

		select_host(api)

		api.select.assert_called_once_with("quiet")

	def test_prefers_cpu_headroom_without_making_it_a_hard_limit(self) -> None:
		api = self._api(
			(self._host("no-cpu", free_cpu_millicores=0), self._host("cpu", free_cpu_millicores=2000))
		)

		select_host(api)

		api.select.assert_called_once_with("cpu")

		api = self._api((self._host("no-cpu", free_cpu_millicores=0),))
		select_host(api)
		api.select.assert_called_once_with("no-cpu")

	def test_sleepy_factor_changes_soft_memory_preference(self) -> None:
		hosts = (
			self._host("sleepy-loaded", is_sleepy=True, sleepy_reserved_memory_mib=4096),
			self._host("roomier", is_sleepy=True, free_memory_mib=5120),
		)
		without_discount = self._api(hosts, is_sleepy=True)
		with_discount = self._api(hosts, is_sleepy=True, factor=2.0)
		awake_request = self._api(hosts, factor=2.0)

		select_host(without_discount)
		select_host(with_discount)
		select_host(awake_request)

		without_discount.select.assert_called_once_with("roomier")
		with_discount.select.assert_called_once_with("sleepy-loaded")
		awake_request.select.assert_called_once_with("roomier")
