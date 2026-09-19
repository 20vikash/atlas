"""Public host placement API."""

from atlas.vm.core.placement.models import PlacementRequirements
from atlas.vm.core.placement.strategies.base import OutOfCapacity, PlacementStrategy, register

__all__ = ["OutOfCapacity", "PlacementRequirements", "PlacementStrategy", "register"]
