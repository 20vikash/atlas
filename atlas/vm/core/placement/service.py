from __future__ import annotations

from typing import TYPE_CHECKING

import frappe
from frappe import _

from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.context import PlacementContext
from atlas.vm.core.placement.strategies import STRATEGIES

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server.metal_server import MetalServer


class PlacementService:
	"""Run the selected strategy for a new VM or an automatic migration."""

	def select_server(
		self,
		request: VirtualMachineCreateRequest,
		architecture: str,
		exclude_servers: set[str] | None = None,
	) -> MetalServer:
		settings = frappe.get_single("Atlas Settings")
		strategy = STRATEGIES.get(settings.placement_strategy)
		if strategy is None:
			frappe.throw(_("Unknown placement strategy: {0}.").format(settings.placement_strategy))

		placement = PlacementContext(
			request, architecture, settings.sleepy_vm_overcommit_factor, exclude_servers
		)
		strategy.select_host(placement)
		return placement.finish()
