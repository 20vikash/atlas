from __future__ import annotations

from typing import TYPE_CHECKING

import frappe
from frappe import _
from frappe.query_builder.functions import Count
from pypika.terms import LiteralValue

from atlas.auth.overrides import get_permission_query_conditions

if TYPE_CHECKING:
	from frappe.model.document import Document

TAG_FIELD = "tags"
DENY_EVERYTHING = "1=0"
MAXIMUM_TAGS = 32
MAXIMUM_TAG_KEY_LENGTH = 128
MAXIMUM_TAG_VALUE_LENGTH = 1000


def validate_tags(document: Document) -> None:
	"""Trim the tag rows of document and reject a key or value that breaks a tag limit."""
	rows = document.get(TAG_FIELD) or []
	if len(rows) > MAXIMUM_TAGS:
		frappe.throw(_("A document takes at most {0} tags.").format(MAXIMUM_TAGS))

	seen: set[str] = set()
	for tag in rows:
		tag.key = (tag.key or "").strip()
		tag.value = (tag.value or "").strip()

		if not tag.key:
			frappe.throw(_("A tag needs a key."))
		if tag.key in seen:
			frappe.throw(_("Tag key {0} is repeated.").format(tag.key))
		if len(tag.key) > MAXIMUM_TAG_KEY_LENGTH:
			frappe.throw(_("A tag key takes at most {0} characters.").format(MAXIMUM_TAG_KEY_LENGTH))
		if len(tag.value) > MAXIMUM_TAG_VALUE_LENGTH:
			frappe.throw(
				_("The value of tag key {0} takes at most {1} characters.").format(
					tag.key, MAXIMUM_TAG_VALUE_LENGTH
				)
			)

		seen.add(tag.key)


def find_names_with_tags(doctype: str, tags: dict[str, str]) -> list[str]:
	"""Return names of documents with every requested tag that the request can read."""
	if not tags:
		return []

	visible = get_permission_query_conditions(doctype=doctype)
	if visible == DENY_EVERYTHING:
		return []

	table = frappe.qb.DocType("Atlas Tag")
	parent = frappe.qb.DocType(doctype)
	key = table.key
	value = table.value
	matches = [(key == tag_key) & (value == tag_value) for tag_key, tag_value in tags.items()]
	wanted = matches[0]
	for match in matches[1:]:
		wanted |= match

	query = (
		frappe.qb.from_(table)
		.join(parent)
		.on(parent.name == table.parent)
		.select(table.parent)
		.where((table.parenttype == doctype) & (table.parentfield == TAG_FIELD) & wanted)
		.groupby(table.parent)
		.having(Count(key).distinct() == len(tags))
	)
	if visible:
		query = query.where(LiteralValue(visible))

	return query.run(pluck=True)


def read_tags(document: Document) -> dict[str, str]:
	"""Return the tags of document as a plain key to value map.

	A query row that did not select the child table has no tags, so it reads empty.
	"""
	return {tag.key: tag.value or "" for tag in getattr(document, TAG_FIELD, None) or []}


def read_tags_for(doctype: str, names: list[str]) -> dict[str, dict[str, str]]:
	"""Return the tags of many documents, keyed by document name.

	A list route reads rows instead of documents, so it loads every tag in one query.
	"""
	if not names:
		return {}

	rows = frappe.get_all(
		"Atlas Tag",
		filters={"parenttype": doctype, "parentfield": TAG_FIELD, "parent": ("in", names)},
		fields=["parent", "key", "value"],
	)
	tags: dict[str, dict[str, str]] = {name: {} for name in names}
	for row in rows:
		tags[row.parent][row.key] = row.value or ""

	return tags
