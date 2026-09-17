from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
	from atlas.vm.core.placement.api import PlacementAPI


def select_host(api: PlacementAPI) -> None:
	"""Keep the current free-capacity order and select the first host that still fits."""
	hosts = sorted(
		api.usage.hosts,
		key=lambda host: (
			host.free.memory_mib,
			host.free.cpu_millicores,
			host.free.storage_mib,
			host.name,
		),
		reverse=True,
	)
	for host in hosts:
		if host.architecture != api.request.architecture:
			continue
		if host.free.memory_mib < api.request.memory_mib or host.free.storage_mib < api.request.disk_mib:
			continue
		if api.select(host.name):
			return
