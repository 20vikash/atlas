from collections.abc import Callable, Mapping
from types import MappingProxyType
from typing import TYPE_CHECKING

from atlas.vm.core.placement.strategies.default import select_host

if TYPE_CHECKING:
	from atlas.vm.core.placement.api import PlacementAPI

STRATEGIES: Mapping[str, Callable[["PlacementAPI"], None]] = MappingProxyType({"Default": select_host})
