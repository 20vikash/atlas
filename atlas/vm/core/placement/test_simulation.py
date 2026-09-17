from dataclasses import replace
from unittest import TestCase

from atlas.vm.core.placement.models import PlacementRequest, Resources
from atlas.vm.core.placement.simulation import Simulation
from atlas.vm.core.placement.strategies import STRATEGIES
from atlas.vm.core.placement.workload import HostType, Scenario, VMShape, VMWorkload, generate_workload


class TestPlacementSimulation(TestCase):
	def setUp(self) -> None:
		self.host_type = HostType("small", "amd64", True, Resources(1000, 2048, 2000))
		self.scenario = Scenario(
			host_types={"small": self.host_type},
			initial_hosts=("small",),
			new_host_type="small",
			shapes=(VMShape(1000, 1024, 100, 1),),
			arrivals_per_day=10,
			mean_lifetime_hours=2,
			mean_activity_minutes=2,
			sleepy_fraction=0.5,
			sleep_after_idle_seconds=10,
			tenant_count=2,
			sleepy_vm_overcommit_factor=1.5,
		)

	def _vm(
		self, identifier: int, arrive_ms: int, *, is_sleepy: bool = False, activity_ms: tuple[int, ...] = ()
	) -> VMWorkload:
		return VMWorkload(
			identifier,
			PlacementRequest(1000, 2048 if is_sleepy else 1024, 100, "amd64", 1, is_sleepy),
			arrive_ms,
			4_000_000,
			activity_ms,
		)

	def test_workload_is_repeatable_and_independent_of_strategy(self) -> None:
		workload = generate_workload(self.scenario, 2, 17)
		self.assertEqual(workload, generate_workload(self.scenario, 2, 17))
		self.assertNotEqual(workload, generate_workload(self.scenario, 2, 18))

		def first_host(api):
			for host in api.usage.hosts:
				if api.select(host.name):
					return

		first = Simulation(self.scenario, workload, 2).run("first", first_host)
		second = Simulation(self.scenario, workload, 2).run("second", first_host)
		self.assertEqual(replace(first, strategy="second"), second)

	def test_registered_default_strategy_runs_against_simulated_api(self) -> None:
		workload = generate_workload(self.scenario, 1, 7)
		result = Simulation(self.scenario, workload, 1).run("Default", STRATEGIES["Default"])
		self.assertEqual(
			result.placed + result.rejected + result.expired_unplaced + result.pending_at_end,
			result.requested,
		)
		self.assertGreater(result.placed, 0)

	def test_pending_host_reuse_and_explicit_extra_count(self) -> None:
		scenario = replace(self.scenario, initial_hosts=())
		workload = (self._vm(0, 0), self._vm(1, 1000))

		def expand(api):
			if api.usage.hosts and api.select(api.usage.hosts[0].name):
				return
			api.spawn_host(count=2)

		result = Simulation(scenario, workload, 1).run("expand", expand)
		self.assertEqual(result.new_hosts, 2)
		self.assertEqual(result.placed, 2)
		self.assertEqual(result.rejected, 0)
		self.assertEqual(result.delayed_placements, 2)
		self.assertEqual(result.wait_max_seconds, 1815)
		self.assertEqual(result.running_p95_seconds, 1817.5)

	def test_usage_and_rate_keep_cpu_advisory(self) -> None:
		workload = (self._vm(0, 0), self._vm(1, 60_000))
		observed = []

		def record(api):
			observed.append((api.placement_rate(), api.placement_rate("host-0001"), api.usage.hosts[0]))
			self.assertTrue(api.select("host-0001"))

		result = Simulation(self.scenario, workload, 1).run("record", record)
		self.assertEqual(result.placed, 2)
		self.assertEqual(result.running, 2)
		self.assertEqual(result.running_p95_seconds, 2.5)
		self.assertEqual(observed[1][0], 0.2)
		self.assertEqual(observed[1][1], 0.2)
		self.assertEqual(observed[1][2].tenant_vm_count, 1)
		self.assertEqual(observed[1][2].free.cpu_millicores, 0)
		self.assertEqual(observed[1][2].free.memory_mib, 1024)

	def test_strategy_can_override_host_type(self) -> None:
		large = HostType("large", "amd64", False, Resources(2000, 4096, 4000))
		scenario = replace(
			self.scenario,
			host_types={"small": self.host_type, "large": large},
			initial_hosts=(),
		)
		virtual_machine = replace(
			self._vm(0, 0),
			request=PlacementRequest(2000, 3072, 100, "amd64", 1, False),
		)

		def choose_large(api):
			for host in api.usage.hosts:
				if api.select(host.name):
					return
			api.spawn_host("large")

		result = Simulation(scenario, (virtual_machine,), 1).run("large", choose_large)
		self.assertEqual(result.placed, 1)
		self.assertEqual(result.new_hosts, 1)
		self.assertEqual(result.rejected, 0)

	def test_pending_request_can_expire_before_host_is_ready(self) -> None:
		scenario = replace(self.scenario, initial_hosts=())
		virtual_machine = replace(self._vm(0, 0), depart_ms=60_000)
		result = Simulation(scenario, (virtual_machine,), 1).run("pending", lambda api: api.spawn_host())
		self.assertEqual(result.expired_unplaced, 1)
		self.assertEqual(result.placed, 0)
		self.assertEqual(result.new_hosts, 1)

	def test_sleep_releases_memory_and_wake_reports_shortfall(self) -> None:
		workload = (self._vm(0, 0, is_sleepy=True, activity_ms=(30_000,)), self._vm(1, 20_000))

		def first_host(api):
			self.assertTrue(api.select("host-0001"))

		result = Simulation(self.scenario, workload, 1).run("first", first_host)
		self.assertEqual(result.placed, 2)
		self.assertGreaterEqual(result.sleeps, 1)
		self.assertEqual(result.wakes, 1)
		self.assertEqual(result.wake_memory_shortfalls, 1)
		self.assertEqual(result.peak_wake_shortfall_mib, 1024)

	def test_activity_cancels_stale_idle_and_save_events(self) -> None:
		virtual_machine = replace(
			self._vm(0, 0, is_sleepy=True, activity_ms=(9_000, 20_000)), depart_ms=34_000
		)

		def first_host(api):
			self.assertTrue(api.select("host-0001"))

		result = Simulation(self.scenario, (virtual_machine,), 1).run("first", first_host)
		self.assertEqual(result.sleeps, 0)

	def test_no_selection_or_host_request_is_rejected(self) -> None:
		scenario = replace(self.scenario, initial_hosts=())
		result = Simulation(scenario, (self._vm(0, 0),), 1).run("none", lambda api: None)
		self.assertEqual(result.rejected, 1)
		self.assertEqual(result.placed, 0)
