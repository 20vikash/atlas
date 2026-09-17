"""Compare registered placement strategies with the same offline workload."""

from __future__ import annotations

import argparse
import json
from dataclasses import asdict
from pathlib import Path

from atlas.vm.core.placement.models import Resources
from atlas.vm.core.placement.simulation import Simulation
from atlas.vm.core.placement.strategies import STRATEGIES
from atlas.vm.core.placement.workload import HostType, Scenario, VMShape, generate_workload

DEFAULT_SCENARIO = Path(__file__).with_name("placement-scenario.json")


def load_scenario(path: Path) -> Scenario:
	"""Read the experiment inputs at the command line boundary."""
	data = json.loads(path.read_text())
	host_types = tuple(
		HostType(
			name=row["name"],
			architecture=row["architecture"],
			is_sleepy=row["is_sleepy"],
			resources=Resources(row["cpu_millicores"], row["memory_mib"], row["storage_mib"]),
		)
		for row in data["host_types"]
	)
	if len({host_type.name for host_type in host_types}) != len(host_types):
		raise ValueError("host_types contains duplicate names")
	scenario = Scenario(
		host_types={host_type.name: host_type for host_type in host_types},
		initial_hosts=tuple(data["initial_hosts"]),
		new_host_type=data["new_host_type"],
		shapes=tuple(VMShape(**shape) for shape in data["shapes"]),
		arrivals_per_day=data["arrivals_per_day"],
		mean_lifetime_hours=data["mean_lifetime_hours"],
		mean_activity_minutes=data["mean_activity_minutes"],
		sleepy_fraction=data["sleepy_fraction"],
		sleep_after_idle_seconds=data["sleep_after_idle_seconds"],
		tenant_count=data["tenant_count"],
		sleepy_vm_overcommit_factor=data["sleepy_vm_overcommit_factor"],
		host_provision_seconds=data.get("host_provision_seconds", 1800.0),
		host_sync_seconds=data.get("host_sync_seconds", 10.0),
		boot_seconds=data.get("boot_seconds", 2.5),
		save_seconds=data.get("save_seconds", 5.5),
		wake_seconds=data.get("wake_seconds", 1.2),
	)
	scenario.validate()
	return scenario


def main() -> None:
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--days", type=int, default=30, help="Simulated days (default: 30)")
	parser.add_argument("--seed", type=int, default=1, help="Workload seed (default: 1)")
	parser.add_argument("--scenario", type=Path, default=DEFAULT_SCENARIO)
	parser.add_argument("--strategy", action="append", choices=tuple(STRATEGIES))
	parser.add_argument("--json", action="store_true", help="Print all metrics as JSON")
	arguments = parser.parse_args()
	try:
		scenario = load_scenario(arguments.scenario)
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
	print(f"{len(workload)} VM requests, {arguments.days} days, seed {arguments.seed}")
	print(
		"Strategy         Placed  Reject  Expired  Pending  Delayed  P95 wait  Hosts  Host hours  Memory  Wake risk"
	)
	for result in results:
		print(
			f"{result.strategy:<16} {result.placed:>6}  {result.rejected:>6}  "
			f"{result.expired_unplaced:>7}  {result.pending_at_end:>7}  "
			f"{result.delayed_placements:>7}  {result.wait_p95_seconds:>7.0f}s  "
			f"{result.hosts_at_end:>5}  {result.host_hours:>10.0f}  "
			f"{result.ready_memory_utilization:>6.1%}  {result.wake_memory_shortfalls:>9}"
		)


if __name__ == "__main__":
	main()
