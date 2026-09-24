from __future__ import annotations

from typing import TYPE_CHECKING, Any

import frappe

from atlas.api.core.base import ApiResult, ListQuery, Page, add_tag_filter, build_page, get_owned_document
from atlas.api.core.docs import api_docs
from atlas.api.core.errors import ResourceNotFound
from atlas.api.models import PublicIPResponse, ReservePublicIPPayload
from atlas.api.router import get_resource_location, public_ips
from atlas.atlas.core.tags import read_tags_for
from atlas.auth.identity import get_current_tenant_id
from atlas.metal_server.core.public_ip_service import PublicIPService

if TYPE_CHECKING:
	from atlas.metal_server.doctype.public_ip_allocation.public_ip_allocation import PublicIPAllocation


def get_owned_public_ip(public_ip_id: str) -> PublicIPAllocation:
	"""Return one public IP that belongs to the request tenant."""
	try:
		return get_owned_document("Public IP Allocation", public_ip_id, "public IP")
	except ResourceNotFound as error:
		raise ResourceNotFound(error.message, code="public_ip_not_found") from None


@public_ips.get("")
@api_docs()
def list_public_ips(query: ListQuery) -> Page[PublicIPResponse]:
	"""List public IPs.

	Returns the tenant's reserved and attached public IPs in newest-first order.
	"""
	filters: dict[str, Any] = {"tenant_id": get_current_tenant_id()}
	if not add_tag_filter("Public IP Allocation", query, filters):
		return build_page([], query)
	rows: list[PublicIPAllocation] = frappe.get_list(
		"Public IP Allocation",
		filters=filters,
		fields=[
			"name",
			"tenant_id",
			"prefix",
			"pool",
			"version",
			"status",
			"is_reserved",
			"virtual_machine",
			"creation",
		],
		order_by="creation desc",
		offset=query.offset,
		limit=query.fetch_limit,
	)
	tags = read_tags_for("Public IP Allocation", [row.name for row in rows])
	return build_page([PublicIPResponse.from_document(row, tags[row.name]) for row in rows], query)


@public_ips.post("")
@api_docs(
	request_example={"version": 6},
	responses={
		201: {"description": "A direct public IP is reserved."},
		409: {"description": "No compatible direct pool has capacity."},
	},
)
def reserve_public_ip(payload: ReservePublicIPPayload) -> ApiResult[PublicIPResponse]:
	"""Reserve new public IP.

	Reserves one direct IPv4 or IPv6 prefix. Atlas selects the pool.
	"""
	allocation = PublicIPService().reserve(get_current_tenant_id(), payload.version)
	return ApiResult(
		PublicIPResponse.from_document(allocation),
		status=201,
		headers={"Location": get_resource_location("public-ips", allocation.name)},
	)


@public_ips.get("<public_ip_id>")
@api_docs()
def get_public_ip(public_ip_id: str) -> PublicIPResponse:
	"""Get public IP.

	Returns one public IP that belongs to the request tenant.
	"""
	return PublicIPResponse.from_document(get_owned_public_ip(public_ip_id))


@public_ips.put("<public_ip_id>/reserve")
@api_docs(
	responses={
		200: {"description": "The direct public IP stays with the tenant after detach."},
		400: {"description": "A routed IPv6 address cannot be reserved."},
	},
)
def reserve_attached_public_ip(public_ip_id: str) -> PublicIPResponse:
	"""Reserve attached public IP.

	Keeps a direct public IP after its next detach.
	"""
	allocation = get_owned_public_ip(public_ip_id)
	allocation.reserve()
	return PublicIPResponse.from_document(allocation)


@public_ips.delete("<public_ip_id>")
@api_docs(
	responses={
		204: {"description": "The detached public IP reservation is released."},
		409: {"description": "The public IP is attached or has an active intent."},
	},
)
def release_public_ip(public_ip_id: str) -> ApiResult[None]:
	"""Release public IP.

	Returns one detached direct public IP to its pool.
	"""
	get_owned_public_ip(public_ip_id).release_reservation()
	return ApiResult(None, status=204)
