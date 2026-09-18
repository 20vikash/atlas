"""Balance lifecycle stability, revenue, and hard-resource pressure."""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
	from atlas.vm.core.placement.api import HostUsage, PlacementAPI


EXPANSION_PRESSURE_LIMIT = 0.8


def select_host(api: PlacementAPI) -> None:
	"""Prefer fitting lifecycle changes in place and expand at 80% pressure."""
	request = api.request
	factor = api.sleepy_vm_overcommit_factor if request.is_sleepy else 1.0
	usage = api.usage

	if api.action != "create" and api.current_host_name:
		current = next(
			(
				host
				for host in usage.hosts
				if host.name == api.current_host_name and host.architecture == request.architecture
			),
			None,
		)
		if current is not None and api.select(current.name):
			return

	used_memory_mib = usage.total.memory_mib - usage.free.memory_mib
	used_storage_mib = usage.total.storage_mib - usage.free.storage_mib
	pressure = max(
		used_memory_mib / usage.total.memory_mib if usage.total.memory_mib else 1.0,
		used_storage_mib / usage.total.storage_mib if usage.total.storage_mib else 1.0,
	)

	def priority(host: HostUsage) -> tuple[bool, int, float, int, float, str]:
		host_used_memory_mib = host.total.memory_mib - host.free.memory_mib
		sleepy_memory_mib = min(host.sleepy_reserved_memory_mib, host_used_memory_mib)
		projected_memory_pressure = (
			host_used_memory_mib - sleepy_memory_mib * (1 - 1 / factor) + request.memory_mib / factor
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
			for host in usage.hosts
			if host.architecture == request.architecture
			and host.free.memory_mib >= request.memory_mib
			and host.free.storage_mib >= request.disk_mib
		),
		key=priority,
	)
	if not hosts or pressure >= EXPANSION_PRESSURE_LIMIT:
		api.spawn_host()

	for host in hosts:
		if api.select(host.name):
			return
