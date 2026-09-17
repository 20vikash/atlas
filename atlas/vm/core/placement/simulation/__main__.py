"""Compare registered placement strategies with the same seeded tenant demand."""

from __future__ import annotations

import argparse
import json
from dataclasses import asdict, replace
from pathlib import Path

from atlas.vm.core.placement.simulation.engine import Simulation
from atlas.vm.core.placement.simulation.workload import (
	Demand,
	HostType,
	Incidents,
	Scenario,
	Traffic,
	VMShape,
	generate_workload,
)
from atlas.vm.core.placement.strategies import STRATEGIES

DEFAULT_SCENARIO = Path(__file__).with_name("scenario.json")


def load_scenario(path: Path) -> Scenario:
	"""Validate configuration at the command line boundary."""
	data = json.loads(path.read_text())
	demand_data = data["demand"].copy()
	for name in ("hour_weights", "weekday_weights", "action_weights"):
		demand_data[name] = tuple(demand_data[name])
	scenario = Scenario(
		host_types=tuple(HostType(**row) for row in data["host_types"]),
		initial_host_count=data["initial_host_count"],
		max_host_count=data["max_host_count"],
		initial_host_type=data["initial_host_type"],
		new_host_type=data["new_host_type"],
		host_start_minutes_min=data["host_start_minutes_min"],
		host_start_minutes_max=data["host_start_minutes_max"],
		cpu_oversubscription=data["cpu_oversubscription"],
		sleepy_vm_overcommit_factor=data["sleepy_vm_overcommit_factor"],
		revenue_multiplier=data["revenue_multiplier"],
		billing_month_days=data["billing_month_days"],
		shapes=tuple(VMShape(**row) for row in data["shapes"]),
		demand=Demand(**demand_data),
		traffic=Traffic(**data["traffic"]),
		incidents=Incidents(**data["incidents"]),
	)
	scenario.validate()
	return scenario


def main() -> None:
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--days", type=int, default=30)
	parser.add_argument("--seed", type=int, default=1)
	parser.add_argument("--scenario", type=Path, default=DEFAULT_SCENARIO)
	parser.add_argument("--cpu-oversubscription", type=float)
	parser.add_argument("--max-host-count", type=int)
	parser.add_argument("--strategy", action="append", choices=tuple(STRATEGIES))
	parser.add_argument("--json", action="store_true", help="Print all metrics as JSON")
	arguments = parser.parse_args()
	try:
		scenario = load_scenario(arguments.scenario)
		if arguments.cpu_oversubscription is not None:
			scenario = replace(scenario, cpu_oversubscription=arguments.cpu_oversubscription)
		if arguments.max_host_count is not None:
			scenario = replace(scenario, max_host_count=arguments.max_host_count)
		workload = generate_workload(scenario, arguments.days, arguments.seed)
	except (OSError, KeyError, TypeError, ValueError) as error:
		parser.error(str(error))
	selected = dict.fromkeys(arguments.strategy or STRATEGIES)
	results = [
		Simulation(scenario, workload, arguments.days).run(name, STRATEGIES[name]) for name in selected
	]
	if arguments.json:
		print(json.dumps([asdict(result) for result in results], indent=2))
		return
	print(f"{len(workload.tenants)} tenants, {arguments.days} days, seed {arguments.seed}")
	print(
		"Strategy         VMs  Hosts  Revenue Rs  Host cost Rs  Margin Rs  Failures  Incidents  Left  Wake moves"
	)
	for result in results:
		failures = (
			result.create_failures + result.resize_failures + result.start_failures + result.wake_failures
		)
		print(
			f"{result.strategy:<16} {result.vms_created:>4}  {result.hosts_at_end:>5}  "
			f"{result.vm_revenue_rupees:>10.0f}  {result.host_cost_rupees:>12.0f}  "
			f"{result.margin_rupees:>9.0f}  {failures:>8}  {result.incidents:>9}  "
			f"{result.tenants_left:>4}  {result.wake_migrations:>10}"
		)


if __name__ == "__main__":
	main()
