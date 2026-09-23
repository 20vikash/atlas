from types import SimpleNamespace
from unittest.mock import MagicMock, patch

from frappe.tests import UnitTestCase

import atlas.metal_server.core.public_ip_service as service_module
from atlas.metal_server.core.public_ip_service import PublicIPService


def pool(name: str, gateway: str):
	return SimpleNamespace(name=name, gateway=gateway, prefix="2001:db8::/64", is_routed=True)


class TestGatewayPoolSelection(UnitTestCase):
	def test_a_gateway_on_the_vm_server_is_preferred(self) -> None:
		pools = {
			"local-pool": pool("local-pool", "local-router"),
			"remote-pool": pool("remote-pool", "remote-router"),
		}

		def router_value(_doctype, gateway, *_args, **_kwargs):
			server = "metal-1" if gateway == "local-router" else "metal-2"
			return SimpleNamespace(status="Active", server=server)

		with (
			patch.object(service_module.frappe, "get_all", return_value=list(pools)),
			patch.object(service_module.frappe, "get_doc", side_effect=lambda _doctype, name: pools[name]),
			patch.object(service_module.frappe.db, "get_value", side_effect=router_value),
			patch.object(service_module.random, "choice", side_effect=lambda values: values[0]),
		):
			selected = PublicIPService()._select_routed_pool(SimpleNamespace(server="metal-1"))

		self.assertEqual(selected.name, "local-pool")

	def test_a_remote_gateway_is_used_when_no_local_gateway_exists(self) -> None:
		remote = pool("remote-pool", "remote-router")
		with (
			patch.object(service_module.frappe, "get_all", return_value=[remote.name]),
			patch.object(service_module.frappe, "get_doc", return_value=remote),
			patch.object(
				service_module.frappe.db,
				"get_value",
				return_value=SimpleNamespace(status="Active", server="metal-2"),
			),
		):
			selected = PublicIPService()._select_routed_pool(SimpleNamespace(server="metal-1"))

		self.assertEqual(selected.name, "remote-pool")


class TestRoutedAttachment(UnitTestCase):
	def test_attach_keeps_unrelated_gateway_routes(self) -> None:
		public_pool = pool("pool-1", "router-1")
		virtual_machine = SimpleNamespace(name="vm-1")
		router = SimpleNamespace(wireguard_mesh_ipv6="fdaa:1::1")
		network_service = MagicMock()
		network_service.get_gateway_routes.return_value = [
			{"destination": "2001:db8:ffff::/48", "gateway": "fdaa:1::2"},
			{"destination": "2000::/3", "gateway": "fdaa:1::9"},
		]

		with (
			patch.object(service_module.frappe, "get_doc", return_value=router),
			patch("atlas.vm.core.vm_service.VirtualMachineService", return_value=network_service),
		):
			PublicIPService()._apply_attach(public_pool, virtual_machine, "2001:db8::1/128", "metal-1")

		network_service.set_gateway_routes.assert_called_once_with(
			[
				{"destination": "2001:db8:ffff::/48", "gateway": "fdaa:1::2"},
				{"destination": "2000::/3", "gateway": "fdaa:1::1"},
			]
		)

	def test_detach_removes_only_the_public_route(self) -> None:
		public_pool = pool("pool-1", "router-1")
		virtual_machine = SimpleNamespace(name="vm-1", is_terminating=0)
		network_service = MagicMock()
		network_service.get_gateway_routes.return_value = [
			{"destination": "2001:db8:ffff::/48", "gateway": "fdaa:1::2"},
			{"destination": "2000::/3", "gateway": "fdaa:1::1"},
		]

		with patch("atlas.vm.core.vm_service.VirtualMachineService", return_value=network_service):
			PublicIPService()._apply_detach(public_pool, virtual_machine)

		network_service.set_gateway_routes.assert_called_once_with(
			[{"destination": "2001:db8:ffff::/48", "gateway": "fdaa:1::2"}]
		)
