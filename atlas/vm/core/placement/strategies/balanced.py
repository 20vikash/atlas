"""Balance lifecycle stability, revenue, and hard-resource pressure."""

from __future__ import annotations

from typing import TYPE_CHECKING

from atlas.vm.core.placement.models import HostUsage
from atlas.vm.core.placement.strategies.base import PlacementStrategy
from atlas.vm.core.placement.strategies.capacity import pool_at_provisioning_limit

if TYPE_CHECKING:
	from atlas.vm.core.placement.context import PlacementContext


EXPANSION_PRESSURE_PERCENT = 80


class BalancedStrategy(PlacementStrategy):
	def select_host(self, placement: PlacementContext) -> None:
		"""Keep each VM in its host pool and expand that pool at 80% pressure."""
		request = placement.request
		factor = placement.sleepy_vm_overcommit_factor if request.is_sleepy else 1.0
		pool = tuple(
			host
			for host in placement.usage.hosts
			if host.architecture == request.architecture and host.is_sleepy == request.is_sleepy
		)

		if placement.action != "create" and placement.current_host_name:
			current = next((host for host in pool if host.name == placement.current_host_name), None)
			if current is not None and placement.select(current.name):
				return

		def priority(host: HostUsage) -> tuple[int, float, int, float, str]:
			host_used_memory_mib = host.total.memory_mib - host.free.memory_mib
			sleepy_memory_mib = min(host.sleepy_reserved_memory_mib, host_used_memory_mib)
			projected_memory_pressure = (
				host_used_memory_mib - sleepy_memory_mib * (1 - 1 / factor) + request.memory_mib / factor
			) / host.total.memory_mib
			projected_storage_pressure = (
				host.total.storage_mib - host.free.storage_mib + request.disk_mib
			) / host.total.storage_mib

			return (
				host.tenant_vm_count,
				placement.placement_rate(host.name),
				max(request.cpu_millicores - host.free.cpu_millicores, 0),
				max(projected_memory_pressure, projected_storage_pressure),
				host.name,
			)

		hosts = sorted(
			(
				host
				for host in pool
				if host.free.memory_mib >= request.memory_mib and host.free.storage_mib >= request.disk_mib
			),
			key=priority,
		)
		if not hosts or pool_at_provisioning_limit(pool, EXPANSION_PRESSURE_PERCENT):
			placement.spawn_host(is_sleepy=request.is_sleepy)

		for host in hosts:
			if placement.select(host.name):
				return
