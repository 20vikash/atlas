"""Resource pressure calculations shared by placement strategies."""

from collections.abc import Sequence

from atlas.vm.core.placement.models import HostUsage, PlacementDemand


def provisioning(host: HostUsage, request: PlacementDemand | None = None) -> float:
	"""Return the larger memory or storage fraction after an optional placement."""
	additional_memory = request.memory_mib if request else 0
	additional_storage = request.disk_mib if request else 0
	return max(
		(host.total.memory_mib - host.free.memory_mib + additional_memory) / host.total.memory_mib,
		(host.total.storage_mib - host.free.storage_mib + additional_storage) / host.total.storage_mib,
	)


def below_provisioning_limit(host: HostUsage, percent: int, request: PlacementDemand | None = None) -> bool:
	"""Check both provisioned resources against one percentage limit."""
	additional_memory = request.memory_mib if request else 0
	additional_storage = request.disk_mib if request else 0
	return (
		host.total.memory_mib - host.free.memory_mib + additional_memory
	) * 100 < host.total.memory_mib * percent and (
		host.total.storage_mib - host.free.storage_mib + additional_storage
	) * 100 < host.total.storage_mib * percent


def pool_at_provisioning_limit(
	hosts: Sequence[HostUsage], percent: int, request: PlacementDemand | None = None
) -> bool:
	"""Check projected aggregate memory or storage pressure for a pool."""
	additional_memory = request.memory_mib if request else 0
	additional_storage = request.disk_mib if request else 0
	total_memory = sum(host.total.memory_mib for host in hosts)
	total_storage = sum(host.total.storage_mib for host in hosts)
	used_memory = sum(host.total.memory_mib - host.free.memory_mib for host in hosts)
	used_storage = sum(host.total.storage_mib - host.free.storage_mib for host in hosts)
	return (used_memory + additional_memory) * 100 >= total_memory * percent or (
		used_storage + additional_storage
	) * 100 >= total_storage * percent


def pool_at_sleepy_subscription_limit(
	hosts: Sequence[HostUsage], percent: int, additional_memory_mib: int
) -> bool:
	"""Count assigned sleepy VM memory, including VMs that are not awake."""
	reserved_memory = sum(host.sleepy_reserved_memory_mib for host in hosts)
	total_memory = sum(host.total.memory_mib for host in hosts)
	return (reserved_memory + additional_memory_mib) * 100 >= total_memory * percent


def sleepy_best_fit(host: HostUsage, request: PlacementDemand) -> tuple[float, float, str]:
	"""Pack subscribed sleepy memory, then provisioned memory and storage."""
	return (
		-(host.sleepy_reserved_memory_mib + request.memory_mib) / host.total.memory_mib,
		-provisioning(host, request),
		host.name,
	)
