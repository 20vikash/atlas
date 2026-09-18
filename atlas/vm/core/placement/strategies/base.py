"""Common interface for host placement strategies."""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import TYPE_CHECKING

if TYPE_CHECKING:
	from atlas.vm.core.placement.context import PlacementContext


class PlacementStrategy(ABC):
	"""Select a host or request capacity for one placement."""

	@abstractmethod
	def select_host(self, placement: PlacementContext) -> None:
		"""Apply this strategy to one placement context."""
