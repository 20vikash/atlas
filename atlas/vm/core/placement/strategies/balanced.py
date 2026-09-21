"""Keep VMs stable and balance new placements across suitable hosts."""

from __future__ import annotations

from typing import override

from atlas.vm.core.placement.models import HostUsage
from atlas.vm.core.placement.strategies.base import PlacementStrategy, register


@register("balanced")
class BalancedStrategy(PlacementStrategy):
	"""Balance tenant isolation, placement rate, CPU shortfall, and resource pressure.

	The strategy keeps a lifecycle operation on its current host when capacity permits.
	For a new placement, it ranks eligible hosts by these keys:

	1. The number of VMs from the same tenant, from low to high.
	2. The placement rate during the last 5 minutes, from low to high.
	3. The projected CPU shortfall, from low to high.
	4. The projected memory or storage pressure, from low to high.
	5. The host name.

	For a sleepy VM, the memory rank applies the configured overcommit factor. The
	locked capacity check does not apply this factor.

	Example: Host A has 2 VMs from the tenant, and host B has 1. Host B ranks first,
	even if host A has a lower placement rate.
	"""

	@override
	def select_host(self) -> None:
		"""Choose a stable host while balancing fleet pressure."""
		host_pool = self.host_pool
		if self.try_current_host(host_pool):
			return

		eligible_hosts = self.get_eligible_hosts(host_pool)
		self.select_from_ranked(sorted(eligible_hosts, key=self._host_priority))

	def _host_priority(self, host: HostUsage) -> tuple[int, float, int, float, str]:
		"""Prefer fewer tenant VMs, then a quieter host, then a better fit."""
		requirements = self.requirements
		factor = self.sleepy_vm_overcommit_factor if requirements.is_sleepy else 1.0
		used_memory_mib = host.total.memory_mib - host.free.memory_mib
		sleepy_memory_mib = min(host.sleepy_reserved_memory_mib, used_memory_mib)
		memory_pressure = (
			used_memory_mib - sleepy_memory_mib * (1 - 1 / factor) + requirements.memory_mib / factor
		) / host.total.memory_mib
		storage_pressure = (
			host.total.storage_mib - host.free.storage_mib + requirements.disk_mib
		) / host.total.storage_mib

		return (
			host.tenant_vm_count,
			self.get_placement_rate(host.name),
			max(requirements.cpu_millicores - host.free.cpu_millicores, 0),
			max(memory_pressure, storage_pressure),
			host.name,
		)
