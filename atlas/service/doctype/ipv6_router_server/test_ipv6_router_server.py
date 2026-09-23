# Copyright (c) 2026, Frappe and Contributors
# See license.txt

from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import frappe
from frappe.tests import UnitTestCase

import atlas.service.doctype.ipv6_router_server.ipv6_router_server as router_module
from atlas.service.doctype.ipv6_router_server.ipv6_router_server import IPv6RouterServer

ROUTER_ROUTE = {"destination": "2000::/3", "gateway": "fdaa:1::1"}
OTHER_ROUTE = {"destination": "2001:db8:ff::/48", "gateway": "fdaa:1::2"}


def block(**values) -> SimpleNamespace:
	defaults = {
		"is_ipv6": True,
		"status": "Allocated",
		"virtual_machine": None,
		"tenant_id": -1,
		"prefix": "2001:db8::/64",
	}
	return SimpleNamespace(**(defaults | values))


def ipv4_address(**values) -> SimpleNamespace:
	defaults = {
		"is_ipv6": False,
		"status": "Allocated",
		"virtual_machine": None,
		"tenant_id": -1,
	}
	return SimpleNamespace(**(defaults | values))


def router(**values) -> SimpleNamespace:
	defaults = {
		"name": "ipv6-router-001",
		"status": "Active",
		"virtual_machine": "vm-00001",
		"wireguard_mesh_ipv6": "fdaa:1::1",
	}
	result = SimpleNamespace(**(defaults | values))
	result.get_public_address = lambda _virtual_machine: "2001:db8::20:5"
	return result


class TestIPv6BlockValidation(UnitTestCase):
	def validate(self, value: SimpleNamespace) -> None:
		with (
			patch.object(router_module.frappe.db, "exists", return_value=True),
			patch.object(router_module.frappe, "get_doc", return_value=value),
		):
			router_module._validate_ipv6_block("block-1")

	def test_a_free_pool_block_is_accepted(self) -> None:
		self.validate(block())

	def test_a_block_that_another_tenant_owns_is_refused(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "no virtual machine holds"):
			self.validate(block(tenant_id=7))

	def test_an_attached_block_is_refused(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "no virtual machine holds"):
			self.validate(block(virtual_machine="vm-00009"))

	def test_a_block_too_small_for_the_host_fields_is_refused(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "/84"):
			self.validate(block(prefix="2001:db8::/96"))


class TestIPv4AddressValidation(UnitTestCase):
	def validate(self, value: SimpleNamespace) -> None:
		with (
			patch.object(router_module.frappe.db, "exists", return_value=True),
			patch.object(router_module.frappe, "get_doc", return_value=value),
		):
			router_module._validate_ipv4_address("address-1")

	def test_a_free_pool_address_is_accepted(self) -> None:
		self.validate(ipv4_address())

	def test_an_ipv6_block_is_refused(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "public IPv4"):
			self.validate(ipv4_address(is_ipv6=True))

	def test_an_attached_address_is_refused(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "no virtual machine holds"):
			self.validate(ipv4_address(virtual_machine="vm-00009"))


class TestIPv6RouterCreation(UnitTestCase):
	def test_router_stores_the_mesh_address_of_its_virtual_machine(self) -> None:
		router_server = SimpleNamespace()

		with patch.object(router_module, "get_virtual_machine_mesh_address", return_value="fdaa:1::1"):
			IPv6RouterServer._set_virtual_machine(router_server, "vm-00001")

		self.assertEqual(router_server.virtual_machine, "vm-00001")
		self.assertEqual(router_server.wireguard_mesh_ipv6, "fdaa:1::1")

	def test_the_router_is_committed_before_vm_placement(self) -> None:
		router_server = MagicMock(name="router_server")
		router_server.name = "ipv6-router-001"
		router_server.flags = SimpleNamespace()
		router_server.status = "Pending"
		calls: list[str] = []
		router_server.insert.side_effect = lambda *_, **__: calls.append("insert")
		router_server._create_virtual_machine.side_effect = lambda *_: (calls.append("create-vm"), False)[1]

		with (
			patch.object(router_module, "_validate_system_manager"),
			patch.object(router_module, "_validate_create_request"),
			patch.object(router_module.frappe, "new_doc", return_value=router_server),
			patch.object(router_module.frappe.db, "commit", side_effect=lambda: calls.append("commit")),
		):
			result = IPv6RouterServer.create({"ipv6_block": "block-1"})

		self.assertEqual(result, {"name": "ipv6-router-001", "is_draft": False})
		self.assertEqual(calls[:3], ["insert", "commit", "create-vm"])
		router_server.enqueue_provisioning.assert_called_once()

	def test_pending_and_interrupted_provisioning_are_queued(self) -> None:
		router_server = MagicMock()
		with (
			patch.object(router_module.frappe, "get_all", return_value=["ipv6-router-001"]) as get_all,
			patch.object(router_module.frappe, "get_doc", return_value=router_server),
		):
			router_module.enqueue_pending_ipv6_router_provisioning()

		self.assertEqual(get_all.call_args.kwargs["filters"]["status"], ["in", ["Pending", "Provisioning"]])
		router_server.enqueue_provisioning.assert_called_once_with(enqueue_after_commit=False)


class TestRoutedVirtualMachine(UnitTestCase):
	def setUp(self) -> None:
		self.service = MagicMock()
		patched = patch("atlas.vm.core.vm_service.VirtualMachineService", return_value=self.service)
		patched.start()
		self.addCleanup(patched.stop)

	def test_attach_replaces_the_internet_route_and_keeps_other_routes(self) -> None:
		self.service.get_gateway_routes.return_value = [
			OTHER_ROUTE,
			{"destination": "2000::/3", "gateway": "vm-00009"},
		]

		address = IPv6RouterServer.attach_virtual_machine(router(), SimpleNamespace())

		self.assertEqual(address, "2001:db8::20:5")
		self.service.set_gateway_routes.assert_called_once_with([OTHER_ROUTE, ROUTER_ROUTE])

	def test_attach_needs_an_active_router(self) -> None:
		with self.assertRaisesRegex(frappe.ValidationError, "not Active"):
			IPv6RouterServer.attach_virtual_machine(router(status="Provisioning"), SimpleNamespace())

	def test_detach_removes_only_the_router_route(self) -> None:
		self.service.get_gateway_routes.return_value = [OTHER_ROUTE, ROUTER_ROUTE]

		with patch.object(router_module, "find_router", return_value=router()):
			router_module.detach_virtual_machine(SimpleNamespace())

		self.service.set_gateway_routes.assert_called_once_with([OTHER_ROUTE])

	def test_detach_without_a_router_route_fails(self) -> None:
		self.service.get_gateway_routes.return_value = [OTHER_ROUTE]

		with self.assertRaisesRegex(frappe.ValidationError, "no routed IPv6"):
			router_module.detach_virtual_machine(SimpleNamespace())


class TestFindRouter(UnitTestCase):
	# The address comes from the block of the router that the VM routes through.
	def test_the_internet_route_gateway_selects_the_router(self) -> None:
		with (
			patch.object(router_module.frappe.db, "get_value", return_value="ipv6-router-002") as get_value,
			patch.object(router_module.frappe, "get_doc", return_value="router-2"),
		):
			self.assertEqual(router_module.find_router([OTHER_ROUTE, ROUTER_ROUTE]), "router-2")

		self.assertEqual(get_value.call_args.args[1]["wireguard_mesh_ipv6"], "fdaa:1::1")

	def test_no_internet_route_means_no_router(self) -> None:
		self.assertIsNone(router_module.find_router([OTHER_ROUTE]))
