from frappe.tests import UnitTestCase
from pydantic import ValidationError as PydanticValidationError

from atlas.api.core.base import DEFAULT_LIST_LIMIT, MAXIMUM_LIST_LIMIT, ListQuery, build_page


class TestListPagination(UnitTestCase):
	def test_default_limit_and_offset(self) -> None:
		query = ListQuery()

		self.assertEqual(query.offset, 0)
		self.assertEqual(query.limit, DEFAULT_LIST_LIMIT)
		self.assertEqual(query.fetch_limit, DEFAULT_LIST_LIMIT + 1)

	def test_limit_bounds_are_enforced(self) -> None:
		self.assertEqual(ListQuery(limit=MAXIMUM_LIST_LIMIT).limit, MAXIMUM_LIST_LIMIT)
		for values in ({"limit": 0}, {"limit": MAXIMUM_LIST_LIMIT + 1}, {"offset": -1}):
			with self.assertRaises(PydanticValidationError):
				ListQuery(**values)

	def test_page_reports_another_page(self) -> None:
		query = ListQuery(limit=2)
		page = build_page(["a", "b", "c"], query)

		self.assertEqual(
			page.model_dump(),
			{"items": ["a", "b"], "offset": 0, "limit": 2, "has_more": True},
		)

	def test_last_page_has_no_more_rows(self) -> None:
		page = build_page(["a", "b"], ListQuery(limit=2, offset=4))

		self.assertEqual(
			page.model_dump(),
			{"items": ["a", "b"], "offset": 4, "limit": 2, "has_more": False},
		)
