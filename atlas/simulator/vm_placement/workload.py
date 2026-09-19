"""Seeded external demand for the offline placement simulator."""

from __future__ import annotations

import random
from dataclasses import dataclass
from math import isfinite, log

TICKS_PER_SECOND = 2
TICKS_PER_MINUTE = 120
TICKS_PER_HOUR = 7200
TICKS_PER_DAY = 172_800
SHAPE_ACTIONS = ("allocate", "stop", "start", "resize", "terminate")


@dataclass(frozen=True, slots=True)
class HostType:
	name: str
	cpu_millicores: int
	memory_mib: int
	storage_mib: int
	monthly_rupees: float
	architecture: str = "amd64"
	is_sleepy: bool = False


@dataclass(frozen=True, slots=True)
class VMShape:
	name: str
	cpu_millicores: int
	memory_mib: int
	disk_mib: int
	weight: float

	@property
	def is_sleepy(self) -> bool:
		return self.name == "sleepy"


@dataclass(frozen=True, slots=True)
class Demand:
	tenants_per_day: float
	hour_weights: tuple[float, ...]
	weekday_weights: tuple[float, ...]
	growth_start: float
	growth_end: float
	target_size_median: float
	target_size_sigma: float
	shape_concentration: float
	actions_per_day_median: float
	action_rate_sigma: float
	burst_probability: float
	burst_max_vms: int
	action_weights: tuple[float, ...]
	global_spikes_per_day: float
	global_tenant_fraction_min: float
	global_tenant_fraction_max: float
	night_tenant_fraction: float
	night_stop_fraction: float
	night_stop_hour: int
	night_start_hour: int


@dataclass(frozen=True, slots=True)
class Traffic:
	visits_per_day: float
	session_median_minutes: float
	session_sigma: float
	waves_per_day: float
	wave_fraction_min: float
	wave_fraction_max: float
	wave_span_seconds_min: float
	wave_span_seconds_max: float
	morning_wave_fraction: float
	idle_minutes: float
	save_seconds: float
	boot_seconds: float
	sleepy_lifetime_median_days: float
	regular_lifetime_median_days: float
	lifetime_sigma: float
	sleepy_upgrade_probability: float
	wake_migration_seconds_min: float
	wake_migration_seconds_max: float
	migration_seconds_per_gib: float
	thin_storage_fraction_min: float
	thin_storage_fraction_max: float


@dataclass(frozen=True, slots=True)
class Incidents:
	group_seconds: float
	terminate_fraction_min: float
	terminate_fraction_max: float
	allocation_multiplier: float
	leave_probability_step: float


@dataclass(frozen=True, slots=True)
class Scenario:
	host_types: tuple[HostType, ...]
	initial_host_count: int
	max_host_count: int
	initial_host_type: str
	new_host_type: str
	host_start_minutes_min: float
	host_start_minutes_max: float
	cpu_oversubscription: float
	sleepy_vm_overcommit_factor: float
	revenue_multiplier: float
	billing_month_days: float
	shapes: tuple[VMShape, ...]
	demand: Demand
	traffic: Traffic
	incidents: Incidents

	def validate(self) -> None:
		if (
			any(
				isinstance(count, bool) or not isinstance(count, int)
				for count in (self.initial_host_count, self.max_host_count)
			)
			or not 0 <= self.initial_host_count <= self.max_host_count
		):
			raise ValueError("initial and maximum host counts must be nonnegative integers")
		if not isfinite(self.cpu_oversubscription) or self.cpu_oversubscription <= 0:
			raise ValueError("CPU oversubscription must be finite and positive")
		if not isfinite(self.sleepy_vm_overcommit_factor) or self.sleepy_vm_overcommit_factor < 1:
			raise ValueError("sleepy VM overcommit must be finite and at least one")
		if not isfinite(self.revenue_multiplier) or self.revenue_multiplier < 0:
			raise ValueError("revenue multiplier must be finite and nonnegative")
		if not isfinite(self.billing_month_days) or self.billing_month_days <= 0:
			raise ValueError("billing month must be positive")
		if (
			not all(isfinite(value) for value in (self.host_start_minutes_min, self.host_start_minutes_max))
			or not 0 < self.host_start_minutes_min <= self.host_start_minutes_max
		):
			raise ValueError("host start time range is invalid")
		hosts = {host.name: host for host in self.host_types}
		if (
			len(hosts) != len(self.host_types)
			or not {self.initial_host_type, self.new_host_type} <= hosts.keys()
		):
			raise ValueError("host types must be unique and include initial and new host types")
		if any(
			any(
				isinstance(value, bool) or not isinstance(value, int) or value <= 0
				for value in (host.cpu_millicores, host.memory_mib, host.storage_mib)
			)
			or not isfinite(host.monthly_rupees)
			or host.monthly_rupees < 0
			for host in self.host_types
		):
			raise ValueError("host resources must be positive and prices nonnegative")
		if not self.shapes or len({shape.name for shape in self.shapes}) != len(self.shapes):
			raise ValueError("VM shapes must be unique and nonempty")
		if any(
			any(
				isinstance(value, bool) or not isinstance(value, int) or value <= 0
				for value in (shape.cpu_millicores, shape.memory_mib, shape.disk_mib)
			)
			or not isfinite(shape.weight)
			or shape.weight < 0
			for shape in self.shapes
		):
			raise ValueError("VM resources must be positive and weights nonnegative")
		if sum(shape.weight for shape in self.shapes) <= 0:
			raise ValueError("VM shape weights must have a positive sum")
		if len(self.demand.hour_weights) != 24 or len(self.demand.weekday_weights) != 7:
			raise ValueError("demand needs 24 hourly and 7 weekday weights")
		if len(self.demand.action_weights) != len(SHAPE_ACTIONS):
			raise ValueError("demand needs five action weights")
		if any(
			not isfinite(value) or value < 0
			for value in (
				*self.demand.hour_weights,
				*self.demand.weekday_weights,
				*self.demand.action_weights,
			)
		):
			raise ValueError("demand weights must be nonnegative")
		if (
			min(
				sum(self.demand.hour_weights),
				sum(self.demand.weekday_weights),
				sum(self.demand.action_weights),
			)
			<= 0
		):
			raise ValueError("demand weights must have positive sums")
		if (
			isinstance(self.demand.burst_max_vms, bool)
			or not isinstance(self.demand.burst_max_vms, int)
			or not 1 <= self.demand.burst_max_vms <= 40
		):
			raise ValueError("burst_max_vms must be an integer between 1 and 40")
		if any(
			isinstance(hour, bool) or not isinstance(hour, int) or not 0 <= hour < 24
			for hour in (self.demand.night_start_hour, self.demand.night_stop_hour)
		):
			raise ValueError("night hours must be integers within a day")
		for name, value in (
			("burst_probability", self.demand.burst_probability),
			("night_tenant_fraction", self.demand.night_tenant_fraction),
			("night_stop_fraction", self.demand.night_stop_fraction),
			("morning_wave_fraction", self.traffic.morning_wave_fraction),
			("sleepy_upgrade_probability", self.traffic.sleepy_upgrade_probability),
		):
			if not isfinite(value) or not 0 <= value <= 1:
				raise ValueError(f"{name} must be between zero and one")
		for lower, upper, name in (
			(
				self.demand.global_tenant_fraction_min,
				self.demand.global_tenant_fraction_max,
				"global tenant fraction",
			),
			(self.traffic.wave_fraction_min, self.traffic.wave_fraction_max, "wave fraction"),
			(
				self.traffic.thin_storage_fraction_min,
				self.traffic.thin_storage_fraction_max,
				"thin storage fraction",
			),
			(
				self.incidents.terminate_fraction_min,
				self.incidents.terminate_fraction_max,
				"incident termination fraction",
			),
		):
			if not all(isfinite(value) for value in (lower, upper)) or not 0 <= lower <= upper <= 1:
				raise ValueError(f"{name} range must be within zero and one")
		for lower, upper, name in (
			(self.traffic.wave_span_seconds_min, self.traffic.wave_span_seconds_max, "wave span"),
			(
				self.traffic.wake_migration_seconds_min,
				self.traffic.wake_migration_seconds_max,
				"wake migration",
			),
		):
			if not all(isfinite(value) for value in (lower, upper)) or not 0 < lower <= upper:
				raise ValueError(f"{name} range is invalid")
		positive = (
			self.demand.tenants_per_day,
			self.demand.growth_start,
			self.demand.growth_end,
			self.demand.target_size_median,
			self.demand.shape_concentration,
			self.demand.actions_per_day_median,
			self.traffic.visits_per_day,
			self.traffic.session_median_minutes,
			self.traffic.idle_minutes,
			self.traffic.boot_seconds,
			self.traffic.sleepy_lifetime_median_days,
			self.traffic.regular_lifetime_median_days,
			self.traffic.migration_seconds_per_gib,
			self.incidents.group_seconds,
		)
		if any(not isfinite(value) or value <= 0 for value in positive):
			raise ValueError("rates and durations must be finite and positive")
		if any(
			not isfinite(value) or value < 0
			for value in (
				self.demand.target_size_sigma,
				self.demand.action_rate_sigma,
				self.demand.global_spikes_per_day,
				self.traffic.session_sigma,
				self.traffic.waves_per_day,
				self.traffic.save_seconds,
				self.traffic.lifetime_sigma,
			)
		):
			raise ValueError("spread and spike values must be finite and nonnegative")
		if (
			not isfinite(self.incidents.allocation_multiplier)
			or not 0 <= self.incidents.allocation_multiplier <= 1
			or not isfinite(self.incidents.leave_probability_step)
			or self.incidents.leave_probability_step < 0
		):
			raise ValueError("incident retention factors are invalid")
		if not any(shape.name == "sleepy" for shape in self.shapes) or not {"a1", "a2"} <= {
			shape.name for shape in self.shapes
		}:
			raise ValueError("sleepy, a1, and a2 shapes are required")

	def host_type(self, name: str) -> HostType:
		host = next((host for host in self.host_types if host.name == name), None)
		if host is None:
			raise ValueError(f"Unknown host type: {name}")
		return host

	def shape(self, name: str) -> VMShape:
		shape = next((shape for shape in self.shapes if shape.name == name), None)
		if shape is None:
			raise ValueError(f"Unknown VM shape: {name}")
		return shape


@dataclass(frozen=True, slots=True)
class TenantProfile:
	identifier: int
	arrive_tick: int
	target_size: int
	shape_weights: tuple[float, ...]
	actions_per_day: float
	burst_probability: float
	is_night_tenant: bool


@dataclass(frozen=True, slots=True)
class ExternalEvent:
	at_tick: int
	identifier: int
	kind: str
	tenant_id: int = 0
	action: str = ""
	count: int = 1
	fraction: float = 0.0
	span_ticks: int = 0


@dataclass(frozen=True, slots=True)
class Workload:
	seed: int
	tenants: tuple[TenantProfile, ...]
	events: tuple[ExternalEvent, ...]


def event_random(seed: int, identifier: int) -> random.Random:
	"""Give one external event a stable random stream across strategies."""
	return random.Random((seed * 6364136223846793005 + identifier * 1442695040888963407) & ((1 << 64) - 1))


def _action(rng: random.Random, demand: Demand) -> str:
	return rng.choices(SHAPE_ACTIONS, weights=demand.action_weights)[0]


def _burst_count(
	rng: random.Random, target_size: int, demand: Demand, probability: float, *, force: bool = False
) -> int:
	if demand.burst_max_vms == 1 or (not force and rng.random() >= probability):
		return 1
	return rng.randint(2, min(40, demand.burst_max_vms, max(2, target_size * 2)))


def generate_workload(scenario: Scenario, days: int, seed: int) -> Workload:
	"""Sample tenant arrivals, intentions, and global shocks once per comparison."""
	if days < 1:
		raise ValueError("days must be positive")
	scenario.validate()
	rng = random.Random(seed)
	horizon = days * TICKS_PER_DAY
	demand = scenario.demand
	events: list[ExternalEvent] = []
	tenants: list[TenantProfile] = []
	sequence = 0

	def add(
		at_tick: int,
		kind: str,
		tenant_id: int = 0,
		action: str = "",
		count: int = 1,
		fraction: float = 0,
		span_ticks: int = 0,
	) -> None:
		nonlocal sequence
		if at_tick < horizon:
			sequence += 1
			events.append(
				ExternalEvent(at_tick, sequence, kind, tenant_id, action, count, fraction, span_ticks)
			)

	hour_mean = sum(demand.hour_weights) / 24
	for hour in range(days * 24):
		start = hour * TICKS_PER_HOUR
		growth = demand.growth_start + (demand.growth_end - demand.growth_start) * start / horizon
		rate = (
			demand.tenants_per_day
			/ 24
			* demand.hour_weights[hour % 24]
			/ hour_mean
			* demand.weekday_weights[(hour // 24) % 7]
			* growth
		)
		if rate == 0:
			continue
		at = start
		while True:
			at += max(1, round(rng.expovariate(rate / TICKS_PER_HOUR)))
			if at >= start + TICKS_PER_HOUR:
				break
			target = max(
				1, round(rng.lognormvariate(log(demand.target_size_median), demand.target_size_sigma))
			)
			weights = tuple(
				rng.gammavariate(max(shape.weight, 0.0001) * demand.shape_concentration, 1)
				for shape in scenario.shapes
			)
			profile = TenantProfile(
				len(tenants) + 1,
				at,
				target,
				weights,
				rng.lognormvariate(log(demand.actions_per_day_median), demand.action_rate_sigma),
				min(1, demand.burst_probability * rng.uniform(0.5, 1.5)),
				rng.random() < demand.night_tenant_fraction,
			)
			tenants.append(profile)
			add(at, "tenant_arrival", profile.identifier, count=min(target, demand.burst_max_vms))
			for offset in range(demand.burst_max_vms, target, demand.burst_max_vms):
				add(
					at + offset // demand.burst_max_vms,
					"tenant_action",
					profile.identifier,
					"allocate",
					min(demand.burst_max_vms, target - offset),
				)
			action_tick = at
			while True:
				action_tick += max(1, round(rng.expovariate(profile.actions_per_day / TICKS_PER_DAY)))
				if action_tick >= horizon:
					break
				add(
					action_tick,
					"tenant_action",
					profile.identifier,
					_action(rng, demand),
					_burst_count(rng, target, demand, profile.burst_probability),
				)
			if profile.is_night_tenant:
				for day in range(at // TICKS_PER_DAY, days):
					for action, hour_of_day in (
						("night_stop", demand.night_stop_hour),
						("night_start", demand.night_start_hour),
					):
						when = day * TICKS_PER_DAY + hour_of_day * TICKS_PER_HOUR
						if when > at:
							add(when, action, profile.identifier, fraction=demand.night_stop_fraction)

	for day in range(days):
		spike_count = _poisson(rng, demand.global_spikes_per_day)
		for _ in range(spike_count):
			at = day * TICKS_PER_DAY + rng.randrange(TICKS_PER_DAY)
			eligible = [tenant for tenant in tenants if tenant.arrive_tick <= at]
			fraction = rng.uniform(demand.global_tenant_fraction_min, demand.global_tenant_fraction_max)
			selected = (
				rng.sample(eligible, min(len(eligible), max(1, round(len(eligible) * fraction))))
				if eligible
				else []
			)
			for tenant in selected:
				add(
					at + rng.randrange(TICKS_PER_MINUTE),
					"tenant_action",
					tenant.identifier,
					_action(rng, demand),
					_burst_count(rng, tenant.target_size, demand, tenant.burst_probability, force=True),
				)
		for _ in range(_poisson(rng, scenario.traffic.waves_per_day)):
			if rng.random() < scenario.traffic.morning_wave_fraction:
				at = day * TICKS_PER_DAY + rng.randrange(6 * TICKS_PER_HOUR, 12 * TICKS_PER_HOUR)
			else:
				at = day * TICKS_PER_DAY + rng.randrange(TICKS_PER_DAY)
			add(
				at,
				"traffic_wave",
				fraction=rng.uniform(scenario.traffic.wave_fraction_min, scenario.traffic.wave_fraction_max),
				span_ticks=round(
					rng.uniform(
						scenario.traffic.wave_span_seconds_min, scenario.traffic.wave_span_seconds_max
					)
					* TICKS_PER_SECOND
				),
			)
	return Workload(
		seed, tuple(tenants), tuple(sorted(events, key=lambda event: (event.at_tick, event.identifier)))
	)


def _poisson(rng: random.Random, expected: float) -> int:
	if expected <= 0:
		return 0
	count = 0
	elapsed = 0.0
	while True:
		elapsed += rng.expovariate(expected)
		if elapsed >= 1:
			return count
		count += 1
