from __future__ import annotations

from typing import TYPE_CHECKING, Any

import frappe

from atlas.api.core.base import (
	ApiResult,
	ListQuery,
	Page,
	add_tag_filter,
	build_page,
	get_owned_document,
)
from atlas.api.core.docs import api_docs
from atlas.api.models import IPAddressResponse, ReserveIPAddressPayload
from atlas.api.router import get_resource_location, ip_addresses
from atlas.atlas.core.tags import read_tags_for
from atlas.auth.identity import get_current_tenant_id
from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import reserve_for_tenant

if TYPE_CHECKING:
	from atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address import (
		MetalServerIPAddress,
	)


def get_owned_ip_address(ip_address_id: str) -> MetalServerIPAddress:
	"""Return one IP address that the request tenant reserved."""
	return get_owned_document("Metal Server IP Address", ip_address_id, "IP address")


@ip_addresses.post("")
@api_docs(
	request_example={"ip_address_id": "203.0.113.10"},
	responses={
		200: {"description": "The held address stays with the tenant."},
		201: {"description": "The address is reserved for the tenant."},
		409: {"description": "The pool is empty or the address is detaching."},
	},
)
def reserve_ip_address(payload: ReserveIPAddressPayload) -> ApiResult[IPAddressResponse]:
	"""Reserve IP address.

	Reserves a shared-pool address, or keeps an address the tenant already holds by naming its ip_address_id.
	"""
	if payload.ip_address_id:
		held_address = get_owned_ip_address(payload.ip_address_id)
		held_address.reserve()
		return ApiResult(IPAddressResponse.from_document(held_address))

	ip_address_name = reserve_for_tenant(get_current_tenant_id())
	ip_address: MetalServerIPAddress = frappe.get_doc("Metal Server IP Address", ip_address_name)

	return ApiResult(
		IPAddressResponse.from_document(ip_address),
		status=201,
		headers={"Location": get_resource_location("ip-addresses", ip_address_name)},
	)


@ip_addresses.get("")
@api_docs()
def list_ip_addresses(query: ListQuery) -> Page[IPAddressResponse]:
	"""List IP addresses.

	Returns one page of IP addresses reserved by the tenant in newest-first order.
	"""
	filters: dict[str, Any] = {"tenant_id": get_current_tenant_id()}
	if not add_tag_filter("Metal Server IP Address", query, filters):
		return build_page([], query)

	rows: list[MetalServerIPAddress] = frappe.get_list(
		"Metal Server IP Address",
		filters=filters,
		fields=["name", "tenant_id", "address", "status", "reserved", "virtual_machine", "creation"],
		order_by="creation desc",
		offset=query.offset,
		limit=query.fetch_limit,
	)
	tags = read_tags_for("Metal Server IP Address", [row.name for row in rows])
	return build_page([IPAddressResponse.from_document(row, tags[row.name]) for row in rows], query)


@ip_addresses.get("<ip_address_id>")
@api_docs()
def get_ip_address(ip_address_id: str) -> IPAddressResponse:
	"""Get IP address.

	Returns one tenant IP address with its attachment state and VM assignment.
	"""
	return IPAddressResponse.from_document(get_owned_ip_address(ip_address_id))


@ip_addresses.delete("<ip_address_id>")
@api_docs(
	responses={
		204: {"description": "The address is back in the shared pool."},
		409: {"description": "The address is attached or detaching."},
	},
)
def release_ip_address(ip_address_id: str) -> None:
	"""Release IP address.

	Returns an unattached address to the shared pool and keeps its provider reservation. An attached or detaching address cannot be released.
	"""
	get_owned_ip_address(ip_address_id).release_to_pool()
