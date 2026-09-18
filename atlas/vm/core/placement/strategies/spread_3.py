"""Keep three regular hosts below the provisioning threshold."""

from __future__ import annotations

from typing import TYPE_CHECKING

from atlas.vm.core.placement.strategies.base import PlacementStrategy
from atlas.vm.core.placement.strategies.capacity import (
	below_provisioning_limit,
	pool_at_sleepy_subscription_limit,
	provisioning,
	sleepy_best_fit,
)

if TYPE_CHECKING:
	from atlas.vm.core.placement.context import PlacementContext


class SpreadThreeStrategy(PlacementStrategy):
	def select_host(self, placement: PlacementContext) -> None:
		"""Spread regular VMs and pack sleepy VMs in separate host pools."""
		request = placement.request
		pool = tuple(
			host
			for host in placement.usage.hosts
			if host.architecture == request.architecture and host.is_sleepy == request.is_sleepy
		)
		if placement.action != "create" and placement.current_host_name:
			current = next((host for host in pool if host.name == placement.current_host_name), None)
			if current is not None and placement.select(current.name):
				return

		eligible = [
			host
			for host in pool
			if host.free.memory_mib >= request.memory_mib and host.free.storage_mib >= request.disk_mib
		]
		if request.is_sleepy:
			eligible.sort(key=lambda host: sleepy_best_fit(host, request))
			additional_memory = request.memory_mib if placement.action == "create" else 0
			if not eligible or pool_at_sleepy_subscription_limit(pool, 90, additional_memory):
				placement.spawn_host(is_sleepy=True)
		else:
			eligible.sort(key=lambda host: (provisioning(host, request), host.name))
			available = sum(below_provisioning_limit(host, 85) for host in pool)
			if placement.action == "create" and eligible:
				chosen = eligible[0]
				if below_provisioning_limit(chosen, 85) and not below_provisioning_limit(chosen, 85, request):
					available -= 1
			missing = max(3 - available, 0)
			if not eligible:
				missing = max(missing, 1)
			if missing:
				placement.spawn_host(count=missing, is_sleepy=False)

		for host in eligible:
			if placement.select(host.name):
				return
