from datetime import datetime
from types import SimpleNamespace
from unittest import TestCase
from unittest.mock import Mock, call

from atlas.vm.core.placement.models import FleetUsage, HostUsage, PlacementDemand, Resources
from atlas.vm.core.placement.strategies.balanced import BalancedStrategy
from atlas.vm.core.placement.strategies.best_fit import BestFitStrategy
from atlas.vm.core.placement.strategies.spread_3 import SpreadThreeStrategy


class TestComparisonStrategies(TestCase):
	@staticmethod
	def _host(
		name: str,
		*,
		architecture: str = "amd64",
		is_sleepy: bool = False,
		free_cpu_millicores: int = 10000,
		used_memory_mib: int = 0,
		used_storage_mib: int = 0,
		tenant_vm_count: int = 0,
		subscribed_memory_mib: int = 0,
	) -> HostUsage:
		return HostUsage(
			name=name,
			architecture=architecture,
			is_sleepy=is_sleepy,
			sample_created_at=datetime(2026, 9, 18),
			total=Resources(10000, 10000, 10000),
			free=Resources(free_cpu_millicores, 10000 - used_memory_mib, 10000 - used_storage_mib),
			tenant_vm_count=tenant_vm_count,
			sleepy_reserved_memory_mib=subscribed_memory_mib,
		)

	@staticmethod
	def _api(
		hosts: tuple[HostUsage, ...],
		*,
		is_sleepy: bool = False,
		factor: float = 1.0,
		rates: dict[str, float] | None = None,
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
			request=PlacementDemand(1000, memory_mib, storage_mib, "amd64", 7, is_sleepy),
			usage=FleetUsage(hosts, total, free, 0, 0),
			action=action,
			current_host_name=current_host_name,
			sleepy_vm_overcommit_factor=factor,
			placement_rate=Mock(side_effect=lambda host_name: (rates or {}).get(host_name, 0.0)),
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

		SpreadThreeStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(count=1, is_sleepy=False)
		api.select.assert_called_once_with("least")

	def test_spread_counts_only_regular_hosts_in_its_reserve(self) -> None:
		api = self._api((self._host("regular"), self._host("sleepy", is_sleepy=True)))

		SpreadThreeStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(count=2, is_sleepy=False)
		api.select.assert_called_once_with("regular")

	def test_best_fit_packs_and_expands_at_projected_pool_threshold(self) -> None:
		api = self._api((self._host("less", used_memory_mib=8000), self._host("more", used_memory_mib=8800)))

		BestFitStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(is_sleepy=False)
		api.select.assert_called_once_with("more")

	def test_sleepy_pool_uses_ninety_or_eighty_five_percent_subscription(self) -> None:
		host = self._host("sleepy", is_sleepy=True, subscribed_memory_mib=8000)
		spread = self._api((host,), is_sleepy=True, memory_mib=500, storage_mib=500)
		best_fit = self._api((host,), is_sleepy=True, memory_mib=500, storage_mib=500)

		SpreadThreeStrategy().select_host(spread)
		BestFitStrategy().select_host(best_fit)

		spread.spawn_host.assert_not_called()
		best_fit.spawn_host.assert_called_once_with(is_sleepy=True)
		spread.select.assert_called_once_with("sleepy")
		best_fit.select.assert_called_once_with("sleepy")

	def test_empty_sleepy_pool_requests_a_marked_host(self) -> None:
		api = self._api((self._host("regular"),), is_sleepy=True)

		SpreadThreeStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(is_sleepy=True)
		api.select.assert_not_called()

	def test_existing_vm_stays_on_its_host_when_it_fits(self) -> None:
		api = self._api(
			(self._host("current", used_memory_mib=8000), self._host("empty")),
			action="start",
			current_host_name="current",
		)

		SpreadThreeStrategy().select_host(api)

		api.select.assert_called_once_with("current")
		api.spawn_host.assert_not_called()

	def test_selection_retries_next_ranked_host(self) -> None:
		api = self._api((self._host("a"), self._host("b", used_memory_mib=1000)))
		api.select.side_effect = [False, True]

		BestFitStrategy().select_host(api)

		self.assertEqual(api.select.call_args_list, [call("b"), call("a")])

	def test_balanced_expands_for_storage_pressure(self) -> None:
		api = self._api((self._host("busy", used_storage_mib=8000),))

		BalancedStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(is_sleepy=False)
		api.select.assert_called_once_with("busy")

	def test_balanced_uses_existing_capacity_below_expansion_threshold(self) -> None:
		api = self._api((self._host("available", used_storage_mib=7999),))

		BalancedStrategy().select_host(api)

		api.spawn_host.assert_not_called()
		api.select.assert_called_once_with("available")

	def test_balanced_keeps_a_fitting_lifecycle_change_on_current_host(self) -> None:
		api = self._api(
			(self._host("current", used_memory_mib=8000), self._host("other")),
			action="start",
			current_host_name="current",
		)

		BalancedStrategy().select_host(api)

		api.select.assert_called_once_with("current")
		api.spawn_host.assert_not_called()

	def test_balanced_requests_sleepy_host_instead_of_regular_host(self) -> None:
		api = self._api((self._host("regular"),), is_sleepy=True)

		BalancedStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(is_sleepy=True)
		api.select.assert_not_called()

	def test_balanced_requests_regular_host_instead_of_sleepy_host(self) -> None:
		api = self._api((self._host("sleepy", is_sleepy=True),))

		BalancedStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(is_sleepy=False)
		api.select.assert_not_called()

	def test_balanced_expands_only_for_matching_pool_pressure(self) -> None:
		api = self._api(
			(self._host("regular", used_storage_mib=9000), self._host("sleepy", is_sleepy=True)),
			is_sleepy=True,
		)

		BalancedStrategy().select_host(api)

		api.spawn_host.assert_not_called()
		api.select.assert_called_once_with("sleepy")

	def test_balanced_expands_sleepy_pool_at_eighty_percent(self) -> None:
		api = self._api(
			(self._host("regular"), self._host("sleepy", is_sleepy=True, used_storage_mib=8000)),
			is_sleepy=True,
		)

		BalancedStrategy().select_host(api)

		api.spawn_host.assert_called_once_with(is_sleepy=True)
		api.select.assert_called_once_with("sleepy")

	def test_balanced_does_not_keep_current_host_from_other_pool(self) -> None:
		api = self._api(
			(self._host("regular"), self._host("sleepy", is_sleepy=True)),
			is_sleepy=True,
			action="start",
			current_host_name="regular",
		)

		BalancedStrategy().select_host(api)

		api.select.assert_called_once_with("sleepy")
		api.spawn_host.assert_not_called()

	def test_balanced_spreads_tenant_before_recent_rate(self) -> None:
		api = self._api(
			(self._host("same-tenant", tenant_vm_count=1), self._host("other-tenant")),
			rates={"other-tenant": 1.0},
		)

		BalancedStrategy().select_host(api)

		api.select.assert_called_once_with("other-tenant")

	def test_balanced_prefers_lower_recent_rate(self) -> None:
		api = self._api(
			(self._host("busy"), self._host("quiet")),
			rates={"busy": 0.8, "quiet": 0.2},
		)

		BalancedStrategy().select_host(api)

		api.select.assert_called_once_with("quiet")

	def test_balanced_retries_eligible_hosts_after_selection_miss(self) -> None:
		api = self._api(
			(
				self._host("first", is_sleepy=True),
				self._host("second", is_sleepy=True),
				self._host("wrong-architecture", architecture="arm64", is_sleepy=True),
				self._host("no-memory", is_sleepy=True, used_memory_mib=9500),
				self._host("no-storage", is_sleepy=True, used_storage_mib=9500),
			),
			is_sleepy=True,
		)
		api.select.side_effect = [False, True]

		BalancedStrategy().select_host(api)

		self.assertEqual(api.select.call_args_list, [call("first"), call("second")])
		api.spawn_host.assert_not_called()

	def test_balanced_prefers_cpu_headroom_without_requiring_it(self) -> None:
		api = self._api((self._host("no-cpu", free_cpu_millicores=0), self._host("cpu")))

		BalancedStrategy().select_host(api)

		api.select.assert_called_once_with("cpu")

		api = self._api((self._host("no-cpu", free_cpu_millicores=0),))
		BalancedStrategy().select_host(api)
		api.select.assert_called_once_with("no-cpu")

	def test_balanced_sleepy_factor_changes_soft_memory_ranking(self) -> None:
		hosts = (
			self._host("sleepy-loaded", is_sleepy=True, used_memory_mib=5000, subscribed_memory_mib=5000),
			self._host("roomier", is_sleepy=True, used_memory_mib=4000),
		)
		without_discount = self._api(hosts, is_sleepy=True)
		with_discount = self._api(hosts, is_sleepy=True, factor=2.0)

		BalancedStrategy().select_host(without_discount)
		BalancedStrategy().select_host(with_discount)

		without_discount.select.assert_called_once_with("roomier")
		with_discount.select.assert_called_once_with("sleepy-loaded")
