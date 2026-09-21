"""Public host placement API."""

from atlas.vm.core.placement.models import PlacementRequirements
from atlas.vm.core.placement.strategies.base import (
	OutOfCapacity,
	PlacementBusy,
	PlacementStrategy,
	register,
)

__all__ = [
	"OutOfCapacity",
	"PlacementBusy",
	"PlacementRequirements",
	"PlacementStrategy",
	"register",
]
