from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.service import PlacementService


class TestPlacementService(UnitTestCase):
	def test_configured_strategy_receives_the_context(self) -> None:
		request = VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7)
		server = SimpleNamespace(name="metal-1")
		api = SimpleNamespace(finish=Mock(return_value=server))
		strategy = Mock()

		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Custom", sleepy_vm_overcommit_factor=1.5),
			),
			patch("atlas.vm.core.placement.service.STRATEGIES", {"Custom": strategy}),
			patch("atlas.vm.core.placement.service.PlacementContext", return_value=api) as placement_api,
		):
			selected = PlacementService().select_server(request, "amd64", {"source"})

		self.assertIs(selected, server)
		placement_api.assert_called_once_with(request, "amd64", 1.5, {"source"})
		strategy.select_host.assert_called_once_with(api)
		api.finish.assert_called_once_with()

	def test_strategy_without_a_selection_reports_no_capacity(self) -> None:
		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="balanced", sleepy_vm_overcommit_factor=1.0),
			),
			patch("atlas.vm.core.placement.context.frappe.get_all", return_value=[]),
			patch("atlas.vm.core.placement.service.STRATEGIES", {"balanced": Mock()}),
			self.assertRaisesRegex(AtlasUserError, "current capacity"),
		):
			PlacementService().select_server(
				VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7), "amd64"
			)

	def test_unknown_configured_strategy_is_rejected(self) -> None:
		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Missing"),
			),
			self.assertRaisesRegex(frappe.ValidationError, "Unknown placement strategy"),
		):
			PlacementService().select_server(
				VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7), "amd64"
			)
