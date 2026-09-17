from types import SimpleNamespace
from unittest.mock import Mock, call, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.exceptions import AtlasUserError
from atlas.vm.core.models import VirtualMachineCreateRequest
from atlas.vm.core.placement.service import PlacementService
from atlas.vm.core.placement.strategies.default import select_host


class TestPlacementService(UnitTestCase):
	def test_configured_strategy_receives_the_api(self) -> None:
		request = VirtualMachineCreateRequest("image", 2000, 2048, 10240, 7)
		server = SimpleNamespace(name="metal-1")
		api = SimpleNamespace(_selected_server=server)
		strategy = Mock()

		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Custom", sleepy_vm_overcommit_factor=1.5),
			),
			patch("atlas.vm.core.placement.service.STRATEGIES", {"Custom": strategy}),
			patch("atlas.vm.core.placement.service.PlacementAPI", return_value=api) as placement_api,
		):
			selected = PlacementService().select_server(request, "amd64", {"source"})

		self.assertIs(selected, server)
		placement_api.assert_called_once_with(request, "amd64", 1.5, {"source"})
		strategy.assert_called_once_with(api)

	def test_strategy_without_a_selection_reports_no_capacity(self) -> None:
		with (
			patch(
				"atlas.vm.core.placement.service.frappe.get_single",
				return_value=SimpleNamespace(placement_strategy="Default", sleepy_vm_overcommit_factor=1.0),
			),
			patch(
				"atlas.vm.core.placement.service.PlacementAPI",
				return_value=SimpleNamespace(_selected_server=None),
			),
			patch("atlas.vm.core.placement.service.STRATEGIES", {"Default": Mock()}),
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


class TestDefaultStrategy(UnitTestCase):
	def test_free_capacity_order_and_retryable_miss(self) -> None:
		hosts = (
			SimpleNamespace(
				name="small",
				architecture="amd64",
				free=SimpleNamespace(memory_mib=2048, cpu_millicores=8000, storage_mib=20480),
			),
			SimpleNamespace(
				name="large",
				architecture="amd64",
				free=SimpleNamespace(memory_mib=4096, cpu_millicores=0, storage_mib=20480),
			),
			SimpleNamespace(
				name="other",
				architecture="arm64",
				free=SimpleNamespace(memory_mib=8192, cpu_millicores=8000, storage_mib=20480),
			),
		)
		api = SimpleNamespace(
			usage=SimpleNamespace(hosts=hosts),
			request=SimpleNamespace(architecture="amd64", memory_mib=1024, disk_mib=10240),
			select=Mock(side_effect=[False, True]),
		)

		select_host(api)

		self.assertEqual(api.select.call_args_list, [call("large"), call("small")])
