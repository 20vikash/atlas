"""Spread regular VMs across hosts and pack sleepy VMs together."""

from __future__ import annotations

from typing import override

from atlas.vm.core.placement.models import HostUsage
from atlas.vm.core.placement.strategies.base import PlacementStrategy, register


@register("spread-3")
class SpreadThreeStrategy(PlacementStrategy):
	"""Spread regular VMs and pack sleepy VMs in separate host pools.

	The strategy keeps a lifecycle operation on its current host when capacity permits.
	It ranks regular hosts by the projected memory or storage use, from low to high.
	It ranks sleepy hosts by projected memory subscription, from high to low. The
	projected memory or storage use is the second key for a sleepy host.
	The host name is the final key for both pools.

	Example: A regular VM gives host A 80% resource use and host B 60% resource use.
	Host B ranks first.
	"""

	@override
	def select_host(self) -> None:
		"""Spread regular VMs and pack sleepy VMs in separate host pools."""
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
		"""Rank a less full regular host before a fuller host."""
		return self.get_provisioning_ratio(host, self.requirements), host.name
