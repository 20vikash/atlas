from collections.abc import Callable
from dataclasses import replace
from unittest import TestCase

from atlas.vm.core.placement.simulation.__main__ import DEFAULT_SCENARIO, load_scenario
from atlas.vm.core.placement.simulation.engine import SimulatedPlacementContext, Simulation
from atlas.vm.core.placement.simulation.workload import (
	ExternalEvent,
	HostType,
	Scenario,
	TenantProfile,
	VMShape,
	Workload,
	generate_workload,
)
from atlas.vm.core.placement.strategies import STRATEGIES
from atlas.vm.core.placement.strategies.base import PlacementStrategy


class ScriptedStrategy(PlacementStrategy):
	def __init__(self, select_host: Callable[[SimulatedPlacementContext], None]) -> None:
		self._select_host = select_host

	def select_host(self, placement: SimulatedPlacementContext) -> None:
		self._select_host(placement)


class TestPlacementSimulation(TestCase):
	def setUp(self) -> None:
		base = load_scenario(DEFAULT_SCENARIO)
		self.shapes = tuple(shape for shape in base.shapes if shape.name in ("sleepy", "a1", "a2"))
		self.scenario = replace(
			base,
			host_types=(HostType("small", 2000, 4096, 100000, 50000),),
			initial_host_type="small",
			new_host_type="small",
			initial_host_count=1,
			max_host_count=2,
			shapes=self.shapes,
			traffic=replace(
				base.traffic,
				visits_per_day=0.0001,
				waves_per_day=0,
				boot_seconds=0.5,
				save_seconds=0.5,
			),
		)

	def _tenant(self, identifier: int, shape: str, target_size: int = 1) -> TenantProfile:
		weights = tuple(1.0 if item.name == shape else 0.0 for item in self.shapes)
		return TenantProfile(identifier, 0, target_size, weights, 1.0, 0.0, False)

	def _workload(
		self, tenants: tuple[TenantProfile, ...], events: tuple[ExternalEvent, ...], seed: int = 1
	) -> Workload:
		return Workload(seed, tenants, events)

	@staticmethod
	def _first_host(api) -> None:
		for host in api.usage.hosts:
			if api.select(host.name):
				return

	def test_scenario_keeps_supplied_fleet_shapes_and_price(self) -> None:
		scenario = load_scenario(DEFAULT_SCENARIO)
		self.assertEqual(scenario.initial_host_count, 30)
		self.assertEqual(scenario.max_host_count, 60)
		self.assertEqual(scenario.host_type("atlas").cpu_millicores, 128000)
		self.assertEqual(scenario.host_type("atlas").memory_mib, 512 * 1024)
		self.assertEqual(scenario.host_type("atlas").storage_mib, round(6.5 * 1024 * 1024))
		self.assertEqual(scenario.host_type("atlas").monthly_rupees, 50000)
		self.assertEqual([shape.weight for shape in scenario.shapes], [45, 18, 15, 10, 7, 5])
		self.assertEqual(scenario.shape("sleepy").disk_mib, 12800)

	def test_host_cap_must_be_at_least_the_initial_fleet(self) -> None:
		with self.assertRaisesRegex(ValueError, "host counts"):
			replace(self.scenario, max_host_count=0).validate()
		with self.assertRaisesRegex(ValueError, "host counts"):
			replace(self.scenario, max_host_count=1.5).validate()

	def test_workload_replays_the_same_external_events(self) -> None:
		base = load_scenario(DEFAULT_SCENARIO)
		first = generate_workload(base, 2, 7)
		self.assertEqual(first, generate_workload(base, 2, 7))
		self.assertNotEqual(first, generate_workload(base, 2, 8))
		self.assertTrue(all(isinstance(event.at_tick, int) for event in first.events))
		self.assertTrue(all(event.count <= 40 for event in first.events))

	def test_stopped_vm_keeps_storage_and_stops_revenue(self) -> None:
		scenario = replace(self.scenario, max_host_count=1)
		half_day = 12 * 60 * 60 * 2
		workload = self._workload(
			(self._tenant(1, "a1"),),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(half_day, 2, "night_stop", tenant_id=1, fraction=1),
			),
		)
		result = Simulation(scenario, workload, 1).run("first", ScriptedStrategy(self._first_host))
		expected = 2 * 3 * 50000 / 4 * (half_day - 1) / (30 * 172800)
		self.assertAlmostEqual(result.vm_revenue_rupees, expected)
		self.assertAlmostEqual(result.host_cost_rupees, 50000 / 30)
		self.assertEqual(result.stopped_at_end, 1)
		self.assertGreater(result.storage_utilization, 0)
		doubled = Simulation(replace(scenario, revenue_multiplier=6), workload, 1).run(
			"first", ScriptedStrategy(self._first_host)
		)
		self.assertAlmostEqual(doubled.vm_revenue_rupees, 2 * result.vm_revenue_rupees)
		self.assertAlmostEqual(doubled.host_cost_rupees, result.host_cost_rupees)

	def test_pending_host_is_shared_and_retries_create(self) -> None:
		scenario = replace(self.scenario, initial_host_count=0, max_host_count=1)
		workload = self._workload(
			(self._tenant(1, "a1"), self._tenant(2, "a1")),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(1, 2, "tenant_arrival", tenant_id=2),
			),
		)
		result = Simulation(scenario, workload, 1).run("balanced", STRATEGIES["balanced"])
		self.assertEqual(result.hosts_at_end, 1)
		self.assertEqual(result.vms_created, 2)
		self.assertEqual(result.create_failures, 0)
		self.assertEqual(result.pending_creates, 0)
		self.assertEqual(result.incidents, 0)
		self.assertAlmostEqual(result.host_cost_rupees, 50000 / 30)

	def test_host_becomes_ready_after_configured_start_time(self) -> None:
		scenario = replace(
			self.scenario,
			initial_host_count=0,
			max_host_count=1,
			host_start_minutes_min=25,
			host_start_minutes_max=25,
		)
		workload = self._workload(
			(self._tenant(1, "a1"), self._tenant(2, "a1"), self._tenant(3, "a1")),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(24 * 120, 2, "tenant_arrival", tenant_id=2),
				ExternalEvent(26 * 120, 3, "tenant_arrival", tenant_id=3),
			),
		)
		result = Simulation(scenario, workload, 1).run("balanced", STRATEGIES["balanced"])
		self.assertEqual(result.create_failures, 1)
		self.assertEqual(result.vms_created, 2)

	def test_create_waiting_at_horizon_is_reported_as_pending(self) -> None:
		scenario = replace(
			self.scenario,
			initial_host_count=0,
			max_host_count=1,
			host_start_minutes_min=1500,
			host_start_minutes_max=1500,
		)
		workload = self._workload(
			(self._tenant(1, "a1"),),
			(ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),),
		)

		result = Simulation(scenario, workload, 1).run("best-fit", STRATEGIES["best-fit"])

		self.assertEqual(result.pending_creates, 1)
		self.assertEqual(result.create_failures, 0)
		self.assertEqual(result.vms_created, 0)

	def test_sleepy_host_is_marked_when_provisioned_and_pending_create_retries(self) -> None:
		scenario = replace(
			self.scenario,
			max_host_count=2,
			host_start_minutes_min=25,
			host_start_minutes_max=25,
		)
		workload = self._workload(
			(self._tenant(1, "sleepy"),),
			(ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),),
		)
		simulation = Simulation(scenario, workload, 1)
		result = simulation.run("spread-3", STRATEGIES["spread-3"])
		self.assertEqual(result.vms_created, 1)
		self.assertEqual(result.create_failures, 0)
		self.assertEqual(result.pending_creates, 0)
		self.assertEqual(result.sleepy_hosts_at_end, 1)
		self.assertTrue(simulation._hosts["host-0002"].is_sleepy)

	def test_sleepy_subscription_counts_stopped_vm_memory(self) -> None:
		sleepy_host = HostType("small", 2000, 4096, 100000, 50000, is_sleepy=True)
		scenario = replace(self.scenario, host_types=(sleepy_host,), max_host_count=1)
		workload = self._workload(
			(self._tenant(1, "sleepy"),),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(3602, 2, "night_stop", tenant_id=1, fraction=1),
			),
		)
		simulation = Simulation(scenario, workload, 1)
		result = simulation.run("spread-3", STRATEGIES["spread-3"])
		self.assertEqual(result.stopped_at_end, 1)
		self.assertEqual(simulation._usage(1).hosts[0].sleepy_reserved_memory_mib, 1024)

	def test_cpu_oversubscription_changes_simulated_admission(self) -> None:
		host = HostType("small", 500, 4096, 100000, 50000)
		scenario = replace(self.scenario, host_types=(host,), max_host_count=1)
		workload = self._workload(
			(self._tenant(1, "a1"), self._tenant(2, "a1")),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(2, 2, "tenant_arrival", tenant_id=2),
			),
		)
		one = Simulation(scenario, workload, 1).run("balanced", STRATEGIES["balanced"])
		two = Simulation(replace(scenario, cpu_oversubscription=2), workload, 1).run(
			"balanced", STRATEGIES["balanced"]
		)
		self.assertEqual((one.vms_created, one.create_failures), (1, 1))
		self.assertEqual((two.vms_created, two.create_failures), (2, 0))

	def test_sleep_releases_resources_and_wake_can_migrate(self) -> None:
		host = HostType("small", 2000, 2048, 100000, 50000)
		scenario = replace(self.scenario, host_types=(host,), initial_host_count=2, max_host_count=2)
		workload = self._workload(
			(self._tenant(1, "sleepy"), self._tenant(2, "a1")),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(3602, 2, "tenant_arrival", tenant_id=2),
				ExternalEvent(3604, 3, "traffic_wave", fraction=1, span_ticks=1),
			),
		)

		def pack_then_move(api):
			name = "host-0002" if api.action == "wake" else "host-0001"
			api.select(name)

		result = Simulation(scenario, workload, 1).run("pack", ScriptedStrategy(pack_then_move))
		self.assertEqual(result.vms_created, 2)
		self.assertGreaterEqual(result.sleeps, 1)
		self.assertEqual(result.wake_migrations, 1)
		self.assertEqual(result.wake_failures, 0)

	def test_wake_without_room_is_a_failure_and_incident(self) -> None:
		host = HostType("small", 2000, 2048, 100000, 50000)
		scenario = replace(self.scenario, host_types=(host,), initial_host_count=1, max_host_count=1)
		workload = self._workload(
			(self._tenant(1, "sleepy"), self._tenant(2, "a1")),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(3602, 2, "tenant_arrival", tenant_id=2),
				ExternalEvent(3604, 3, "traffic_wave", fraction=1, span_ticks=1),
			),
		)
		result = Simulation(scenario, workload, 1).run("first", ScriptedStrategy(self._first_host))
		self.assertEqual(result.wake_failures, 1)
		self.assertEqual(result.incidents, 1)
		self.assertEqual(result.vms_terminated, 1)

	def test_incidents_group_failures_and_can_make_tenant_leave(self) -> None:
		scenario = replace(
			self.scenario,
			initial_host_count=0,
			max_host_count=0,
			incidents=replace(self.scenario.incidents, allocation_multiplier=1, leave_probability_step=1),
		)
		workload = self._workload(
			(self._tenant(1, "a1", 3),),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(100, 2, "tenant_action", tenant_id=1, action="allocate"),
				ExternalEvent(130, 3, "tenant_action", tenant_id=1, action="allocate"),
			),
		)
		result = Simulation(scenario, workload, 1).run("balanced", STRATEGIES["balanced"])
		self.assertEqual(result.create_failures, 3)
		self.assertEqual(result.incidents, 2)
		self.assertEqual(result.tenants_left, 1)

	def test_strategy_sees_sleepy_factor_and_current_host(self) -> None:
		workload = self._workload(
			(self._tenant(1, "a1"),),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(10, 2, "night_stop", tenant_id=1, fraction=1),
				ExternalEvent(20, 3, "night_start", tenant_id=1, fraction=1),
			),
		)
		seen = []

		def stay(api):
			seen.append((api.action, api.current_host_name, api.sleepy_vm_overcommit_factor))
			api.select(api.current_host_name or api.usage.hosts[0].name)

		result = Simulation(self.scenario, workload, 1).run("stay", ScriptedStrategy(stay))
		self.assertEqual(result.start_migrations, 0)
		self.assertEqual(seen, [("create", None, 1.5), ("start", "host-0001", 1.5)])

	def test_resize_target_reserves_new_shape_during_migration(self) -> None:
		host = HostType("small", 2000, 2048, 100000, 50000)
		scenario = replace(self.scenario, host_types=(host,), initial_host_count=2, max_host_count=2)
		resizing_tenant = replace(self._tenant(1, "a1"), shape_weights=(0, 100, 1))
		workload = self._workload(
			(resizing_tenant, self._tenant(2, "a1")),
			(
				ExternalEvent(0, 1, "tenant_arrival", tenant_id=1),
				ExternalEvent(10, 2, "tenant_action", tenant_id=1, action="resize"),
				ExternalEvent(11, 3, "tenant_arrival", tenant_id=2),
			),
		)

		def move(api):
			api.select("host-0002" if api.action == "resize" or api.request.tenant_id == 2 else "host-0001")

		result = Simulation(scenario, workload, 1).run("move", ScriptedStrategy(move))
		self.assertEqual(result.resize_migrations, 1)
		self.assertEqual(result.create_failures, 1)

	def test_same_workload_and_strategy_replays_identically(self) -> None:
		workload = generate_workload(load_scenario(DEFAULT_SCENARIO), 1, 17)
		scenario = load_scenario(DEFAULT_SCENARIO)
		first = Simulation(scenario, workload, 1).run("balanced", STRATEGIES["balanced"])
		second = Simulation(scenario, workload, 1).run("balanced", STRATEGIES["balanced"])
		self.assertEqual(first, second)
