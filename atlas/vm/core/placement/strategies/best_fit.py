"""Pack VMs onto eligible hosts with the highest projected resource use."""

from __future__ import annotations

from typing import override

from atlas.vm.core.placement.models import HostUsage
from atlas.vm.core.placement.strategies.base import PlacementStrategy, register


@register("best-fit")
class BestFitStrategy(PlacementStrategy):
	"""Pack VMs to keep other hosts available for larger requests.

	The strategy keeps a lifecycle operation on its current host when capacity permits.
	It ranks regular hosts by the projected memory or storage use, from high to low.
	It ranks sleepy hosts by projected memory subscription, from high to low. The
	projected memory or storage use is the second key for a sleepy host.
	The host name is the final key for both pools.

	Example: A regular VM gives host A 80% resource use and host B 60% resource use.
	Host A ranks first.
	"""

	@override
	def select_host(self) -> None:
		"""Choose the fullest eligible host in the VM's pool."""
		requirements = self.requirements
		host_pool = self.host_pool
		if self.try_current_host(host_pool):
			return

		eligible_hosts = self.get_eligible_hosts(host_pool)
		if requirements.is_sleepy:
			eligible_hosts.sort(key=self.get_sleepy_host_priority)
		else:
			eligible_hosts.sort(key=self._regular_host_priority)

		self.select_from_ranked(eligible_hosts)

	def _regular_host_priority(self, host: HostUsage) -> tuple[float, str]:
		"""Rank a fuller regular host before a less full host."""
		return -self.get_provisioning_ratio(host, self.requirements), host.name
