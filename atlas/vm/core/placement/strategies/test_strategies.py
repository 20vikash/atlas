from datetime import datetime
from types import SimpleNamespace
from unittest import TestCase
from unittest.mock import Mock, call

from atlas.vm.core.placement.models import FleetUsage, HostUsage, PlacementRequest, Resources
from atlas.vm.core.placement.strategies.balanced import select_host as select_balanced
from atlas.vm.core.placement.strategies.best_fit import select_host as select_best_fit
from atlas.vm.core.placement.strategies.spread_3 import select_host as select_spread_3


class TestComparisonStrategies(TestCase):
	@staticmethod
	def _host(
		name: str,
		*,
		is_sleepy: bool = False,
		used_memory_mib: int = 0,
		used_storage_mib: int = 0,
		subscribed_memory_mib: int = 0,
	) -> HostUsage:
		return HostUsage(
			name=name,
			architecture="amd64",
			is_sleepy=is_sleepy,
			sample_created_at=datetime(2026, 9, 18),
			total=Resources(10000, 10000, 10000),
			free=Resources(10000, 10000 - used_memory_mib, 10000 - used_storage_mib),
			tenant_vm_count=0,
			sleepy_reserved_memory_mib=subscribed_memory_mib,
		)

	@staticmethod
	def _api(
		hosts: tuple[HostUsage, ...],
		*,
		is_sleepy: bool = False,
		memory_mib: int = 1000,
		storage_mib: int = 1000,
		action: str = "create",
		current_host_name: str | None = None,
	) -> SimpleNamespace:
		total = Resources(
			sum(host.total.cpu_millicores for host in hosts),
			sum(host.total.memory_mib for host in hosts),
			sum(host.total.storage_mib for host in hosts),
		)
		free = Resources(
			sum(host.free.cpu_millicores for host in hosts),
			sum(host.free.memory_mib for host in hosts),
			sum(host.free.storage_mib for host in hosts),
		)
		return SimpleNamespace(
			request=PlacementRequest(1000, memory_mib, storage_mib, "amd64", 7, is_sleepy),
			usage=FleetUsage(hosts, total, free, 0, 0),
			action=action,
			current_host_name=current_host_name,
			placement_rate=Mock(return_value=0.0),
			select=Mock(return_value=True),
			spawn_host=Mock(),
		)

	def test_spread_orders_one_host_when_placement_uses_third_reserve(self) -> None:
		api = self._api(
			(
				self._host("fuller", used_memory_mib=8400),
				self._host("middle", used_memory_mib=8300),
				self._host("least", used_memory_mib=8200),
			)
		)

		select_spread_3(api)

		api.spawn_host.assert_called_once_with(count=1, is_sleepy=False)
		api.select.assert_called_once_with("least")

	def test_spread_counts_only_regular_hosts_in_its_reserve(self) -> None:
		api = self._api((self._host("regular"), self._host("sleepy", is_sleepy=True)))

		select_spread_3(api)

		api.spawn_host.assert_called_once_with(count=2, is_sleepy=False)
		api.select.assert_called_once_with("regular")

	def test_best_fit_packs_and_expands_at_projected_pool_threshold(self) -> None:
		api = self._api((self._host("less", used_memory_mib=8000), self._host("more", used_memory_mib=8800)))

		select_best_fit(api)

		api.spawn_host.assert_called_once_with(is_sleepy=False)
		api.select.assert_called_once_with("more")

	def test_sleepy_pool_uses_ninety_or_eighty_five_percent_subscription(self) -> None:
		host = self._host("sleepy", is_sleepy=True, subscribed_memory_mib=8000)
		spread = self._api((host,), is_sleepy=True, memory_mib=500, storage_mib=500)
		best_fit = self._api((host,), is_sleepy=True, memory_mib=500, storage_mib=500)

		select_spread_3(spread)
		select_best_fit(best_fit)

		spread.spawn_host.assert_not_called()
		best_fit.spawn_host.assert_called_once_with(is_sleepy=True)
		spread.select.assert_called_once_with("sleepy")
		best_fit.select.assert_called_once_with("sleepy")

	def test_empty_sleepy_pool_requests_a_marked_host(self) -> None:
		api = self._api((self._host("regular"),), is_sleepy=True)

		select_spread_3(api)

		api.spawn_host.assert_called_once_with(is_sleepy=True)
		api.select.assert_not_called()

	def test_existing_vm_stays_on_its_host_when_it_fits(self) -> None:
		api = self._api(
			(self._host("current", used_memory_mib=8000), self._host("empty")),
			action="start",
			current_host_name="current",
		)

		select_spread_3(api)

		api.select.assert_called_once_with("current")
		api.spawn_host.assert_not_called()

	def test_selection_retries_next_ranked_host(self) -> None:
		api = self._api((self._host("a"), self._host("b", used_memory_mib=1000)))
		api.select.side_effect = [False, True]

		select_best_fit(api)

		self.assertEqual(api.select.call_args_list, [call("b"), call("a")])

	def test_balanced_expands_for_storage_pressure(self) -> None:
		api = self._api((self._host("busy", used_storage_mib=7750),))

		select_balanced(api)

		api.spawn_host.assert_called_once_with()
		api.select.assert_called_once_with("busy")

	def test_balanced_uses_existing_capacity_below_expansion_threshold(self) -> None:
		api = self._api((self._host("available", used_storage_mib=7749),))

		select_balanced(api)

		api.spawn_host.assert_not_called()
		api.select.assert_called_once_with("available")

	def test_balanced_keeps_a_fitting_lifecycle_change_on_current_host(self) -> None:
		api = self._api(
			(self._host("current", used_memory_mib=8000), self._host("other")),
			action="start",
			current_host_name="current",
		)

		select_balanced(api)

		api.select.assert_called_once_with("current")
		api.spawn_host.assert_not_called()
