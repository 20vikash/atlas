from __future__ import annotations

from typing import Any

from atlas.api.core.errors import InvalidRequest
from atlas.auth.roles import has_role
from atlas.auth.tenant import get_tenant_id

TENANT_DOCUMENT_TYPES = {
	"Metal Server IP Address",
	"Virtual Machine",
	"Virtual Machine Image",
}


def get_permission_query_conditions(user: str | None = None, doctype: str | None = None) -> str:
	"""Return the list condition for one Atlas DocType."""
	if has_role("System Manager", user):
		return ""
	if doctype not in TENANT_DOCUMENT_TYPES:
		return "1=0"
	try:
		tenant_id = get_tenant_id()
	except InvalidRequest:
		return "1=0"

	query = f"`tab{doctype}`.`tenant_id` = {tenant_id}"
	if doctype == "Virtual Machine Image":
		return f"({query} OR `tabVirtual Machine Image`.`image_type` = 'System')"
	return query


def has_permission(doc: Any, ptype: str, user: str | None = None, debug: bool = False) -> bool:
	"""Return whether a user can access one Atlas document."""
	if has_role("System Manager", user):
		return True
	if doc.doctype not in TENANT_DOCUMENT_TYPES:
		return False
	try:
		tenant_id = get_tenant_id()
	except InvalidRequest:
		return False
	if doc.doctype == "Virtual Machine Image" and ptype == "read" and doc.is_shared:
		return True
	return doc.tenant_id == tenant_id
