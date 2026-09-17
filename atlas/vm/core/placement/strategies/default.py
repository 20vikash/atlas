from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
	from atlas.vm.core.placement.api import HostUsage, PlacementAPI


def select_host(api: PlacementAPI) -> None:
	"""Spread tenants and bursts while keeping memory and storage within capacity."""
	request = api.request
	factor = api.sleepy_vm_overcommit_factor if request.is_sleepy else 1.0
	usage = api.usage
	should_expand = (
		not usage.hosts
		or (
			usage.total.memory_mib > 0
			and (usage.total.memory_mib - usage.free.memory_mib) * 5 >= usage.total.memory_mib * 4
		)
		or (
			usage.total.cpu_millicores > 0
			and (usage.total.cpu_millicores - usage.free.cpu_millicores) * 5 >= usage.total.cpu_millicores * 4
		)
	)

	def priority(host: HostUsage) -> tuple[bool, int, float, int, float, str]:
		used_memory_mib = host.total.memory_mib - host.free.memory_mib
		# Discount only sleepy memory that the sample still reports as used.
		sleepy_memory_mib = min(host.sleepy_reserved_memory_mib, used_memory_mib)
		projected_memory_pressure = (
			used_memory_mib - sleepy_memory_mib * (1 - 1 / factor) + request.memory_mib / factor
		) / host.total.memory_mib
		projected_storage_pressure = (
			host.total.storage_mib - host.free.storage_mib + request.disk_mib
		) / host.total.storage_mib

		return (
			host.is_sleepy != request.is_sleepy,
			host.tenant_vm_count,
			api.placement_rate(host.name),
			max(request.cpu_millicores - host.free.cpu_millicores, 0),
			max(projected_memory_pressure, projected_storage_pressure),
			host.name,
		)

	hosts = sorted(
		(
			host
			for host in api.usage.hosts
			if host.architecture == request.architecture
			and host.free.memory_mib >= request.memory_mib
			and host.free.storage_mib >= request.disk_mib
		),
		key=priority,
	)
	if should_expand or not hosts:
		api.spawn_host()

	for host in hosts:
		if api.select(host.name):
			return
