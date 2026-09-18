from collections.abc import Callable, Mapping
from types import MappingProxyType
from typing import TYPE_CHECKING

from atlas.vm.core.placement.strategies.balanced import select_host as select_balanced
from atlas.vm.core.placement.strategies.best_fit import select_host as select_best_fit
from atlas.vm.core.placement.strategies.default import select_host
from atlas.vm.core.placement.strategies.spread_3 import select_host as select_spread_3

if TYPE_CHECKING:
	from atlas.vm.core.placement.api import PlacementAPI

STRATEGIES: Mapping[str, Callable[["PlacementAPI"], None]] = MappingProxyType(
	{
		"Default": select_host,
		"balanced": select_balanced,
		"spread-3": select_spread_3,
		"best-fit": select_best_fit,
	}
)
