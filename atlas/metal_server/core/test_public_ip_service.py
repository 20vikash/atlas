from types import SimpleNamespace
from unittest.mock import MagicMock, patch

from frappe.tests import UnitTestCase

import atlas.metal_server.core.public_ip_service as service_module
from atlas.metal_server.core.public_ip_service import PublicIPService
from atlas.vm.core.models import Route
from atlas.vm.core.vm_service import VirtualMachineService


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


UNRELATED_ROUTE = Route("2001:db8:ffff::/48", "fdaa:1::2")


def direct_pool(version: str):
	return SimpleNamespace(
		name="pool-1", gateway=None, is_routed=False, source="Static", version=version, host_address=None
	)


class TestRouteAttachment(UnitTestCase):
	def attach(self, public_pool, prefix: str, routes: list[Route]) -> MagicMock:
		router = SimpleNamespace(wireguard_mesh_ipv6="fdaa:1::1")
		with (
			patch.object(service_module.frappe, "get_doc", return_value=router),
			patch.object(VirtualMachineService, "get_routes", return_value=routes),
			patch.object(VirtualMachineService, "update_network") as update_network,
		):
			PublicIPService()._apply_attach(public_pool, SimpleNamespace(name="vm-1"), prefix, "metal-1")
		return update_network

	def detach(self, public_pool, routes: list[Route]) -> MagicMock:
		with (
			patch.object(VirtualMachineService, "get_routes", return_value=routes),
			patch.object(VirtualMachineService, "update_network") as update_network,
		):
			PublicIPService()._apply_detach(public_pool, SimpleNamespace(name="vm-1", is_terminating=0))
		return update_network

	def test_routed_attach_replaces_the_internet_route_and_keeps_other_routes(self) -> None:
		update_network = self.attach(
			pool("pool-1", "router-1"), "2001:db8::1/128", [UNRELATED_ROUTE, Route("2000::/3", "fdaa:1::9")]
		)

		update_network.assert_called_once_with(
			{"routes": [UNRELATED_ROUTE.as_dict(), {"destination": "2000::/3", "via": "fdaa:1::1"}]}
		)

	def test_direct_ipv6_attach_sets_the_address_and_its_host_route(self) -> None:
		update_network = self.attach(direct_pool("6"), "2001:db8::7/128", [UNRELATED_ROUTE])

		update_network.assert_called_once_with(
			{
				"public_ipv6": "2001:db8::7/128",
				"routes": [UNRELATED_ROUTE.as_dict(), {"destination": "2000::/3", "via": "host"}],
			}
		)

	def test_direct_ipv4_attach_sets_the_address_and_its_host_route(self) -> None:
		update_network = self.attach(direct_pool("4"), "203.0.113.7/32", [])

		update_network.assert_called_once_with(
			{"public_ipv4": "203.0.113.7", "routes": [{"destination": "0.0.0.0/0", "via": "host"}]}
		)

	def test_routed_detach_removes_only_the_internet_route(self) -> None:
		update_network = self.detach(
			pool("pool-1", "router-1"), [UNRELATED_ROUTE, Route("2000::/3", "fdaa:1::1")]
		)

		update_network.assert_called_once_with({"routes": [UNRELATED_ROUTE.as_dict()]})

	def test_direct_ipv6_detach_removes_the_address_and_its_host_route(self) -> None:
		update_network = self.detach(direct_pool("6"), [UNRELATED_ROUTE, Route("2000::/3", "host")])

		update_network.assert_called_once_with({"public_ipv6": "", "routes": [UNRELATED_ROUTE.as_dict()]})
