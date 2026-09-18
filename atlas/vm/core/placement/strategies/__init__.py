from collections.abc import Mapping
from types import MappingProxyType

from atlas.vm.core.placement.strategies.balanced import BalancedStrategy
from atlas.vm.core.placement.strategies.base import PlacementStrategy
from atlas.vm.core.placement.strategies.best_fit import BestFitStrategy
from atlas.vm.core.placement.strategies.spread_3 import SpreadThreeStrategy

STRATEGIES: Mapping[str, PlacementStrategy] = MappingProxyType(
	{
		"balanced": BalancedStrategy(),
		"spread-3": SpreadThreeStrategy(),
		"best-fit": BestFitStrategy(),
	}
)
