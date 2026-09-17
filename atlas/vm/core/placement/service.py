from __future__ import annotations

from typing import TYPE_CHECKING

import frappe
from frappe import _

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.api import PlacementAPI
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

		api = PlacementAPI(request, architecture, settings.sleepy_vm_overcommit_factor, exclude_servers)
		strategy(api)
		if api._selected_server is None:
			frappe.throw(
				_("No Metal Server has current capacity for this Virtual Machine."), exc=AtlasUserError
			)

		return api._selected_server
