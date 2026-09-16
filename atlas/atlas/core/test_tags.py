import frappe
from frappe.tests import IntegrationTestCase, UnitTestCase

from atlas.api.core.base import parse_tag_filter
from atlas.api.core.errors import InvalidRequest
from atlas.api.tests.test_support import OTHER_TENANT_ID, TENANT_ID, api_request
from atlas.atlas.core.tags import find_names_with_tags, read_tags_for


def insert_image(title: str, tags: list[dict[str, str]], **overrides) -> str:
	"""Insert one tagged System image and return its name."""
	values = {
		"doctype": "Virtual Machine Image",
		"title": title,
		"image_type": "system",
		"architecture": "amd64",
		"status": "Pending",
		"tags": tags,
	}
	values.update(overrides)
	return frappe.get_doc(values).insert().name


class TestTagValidation(IntegrationTestCase):
	def test_a_repeated_key_is_rejected(self) -> None:
		with self.assertRaises(frappe.ValidationError):
			insert_image("repeated-key", [{"key": "os", "value": "a"}, {"key": "os", "value": "b"}])

	def test_an_empty_key_is_rejected(self) -> None:
		with self.assertRaises(frappe.ValidationError):
			insert_image("empty-key", [{"key": "  ", "value": "a"}])

	def test_a_key_and_value_lose_their_surrounding_space(self) -> None:
		name = insert_image("trimmed", [{"key": " os ", "value": " Ubuntu "}])

		self.assertEqual(read_tags_for("Virtual Machine Image", [name]), {name: {"os": "Ubuntu"}})


class TestTagSearch(IntegrationTestCase):
	"""Each test uses its own suite value, so stored images never overlap."""

	def build_pair(self, suite: str) -> tuple[str, str]:
		"""Return one image with two tags and one image with the suite tag alone."""
		both = insert_image(
			f"both-tags-{suite}", [{"key": "suite", "value": suite}, {"key": "channel", "value": "lts"}]
		)
		one = insert_image(f"one-tag-{suite}", [{"key": "suite", "value": suite}])
		return both, one

	def test_every_requested_tag_must_match(self) -> None:
		both, _one = self.build_pair("every")

		names = find_names_with_tags("Virtual Machine Image", {"suite": "every", "channel": "lts"})

		self.assertEqual(names, [both])

	def test_one_tag_matches_every_carrier(self) -> None:
		both, one = self.build_pair("carriers")

		names = find_names_with_tags("Virtual Machine Image", {"suite": "carriers"})

		self.assertEqual(set(names), {both, one})

	def test_an_unknown_value_matches_nothing(self) -> None:
		self.build_pair("unknown")

		self.assertEqual(find_names_with_tags("Virtual Machine Image", {"suite": "absent"}), [])

	def test_a_tag_does_not_reach_another_doctype(self) -> None:
		self.build_pair("doctype")

		self.assertEqual(find_names_with_tags("Virtual Machine", {"suite": "doctype"}), [])


class TestTagSearchScope(IntegrationTestCase):
	"""A tag search respects each tenant's read permissions."""

	def setUp(self) -> None:
		self.suite = [{"key": "suite", "value": "scope"}]
		self.own = insert_image("own-image", self.suite, image_type="machine", tenant_id=TENANT_ID)
		self.other = insert_image("other-image", self.suite, image_type="machine", tenant_id=OTHER_TENANT_ID)
		self.shared = insert_image("shared-image", self.suite, tenant_id=OTHER_TENANT_ID)

	def search(self, tenant_id: int) -> set[str]:
		"""Search the suite tag as one tenant."""
		with api_request("GET", "/api/atlas/images", tenant_id=tenant_id):
			return set(find_names_with_tags("Virtual Machine Image", {"suite": "scope"}))

	def test_a_tenant_does_not_collect_the_machine_image_of_another(self) -> None:
		names = self.search(TENANT_ID)

		self.assertIn(self.own, names)
		self.assertNotIn(self.other, names)

	def test_a_shared_system_image_stays_visible_to_every_tenant(self) -> None:
		self.assertIn(self.shared, self.search(TENANT_ID))
		self.assertIn(self.shared, self.search(OTHER_TENANT_ID))


class TestTagFilterParsing(UnitTestCase):
	def test_pairs_are_read_and_trimmed(self) -> None:
		self.assertEqual(parse_tag_filter(" os : Ubuntu , channel:lts"), {"os": "Ubuntu", "channel": "lts"})

	def test_no_filter_reads_empty(self) -> None:
		self.assertEqual(parse_tag_filter(None), {})

	def test_an_empty_value_is_allowed(self) -> None:
		self.assertEqual(parse_tag_filter("retired:"), {"retired": ""})

	def test_a_pair_without_a_separator_is_rejected(self) -> None:
		with self.assertRaises(InvalidRequest):
			parse_tag_filter("os")

	def test_a_repeated_key_is_rejected(self) -> None:
		with self.assertRaises(InvalidRequest):
			parse_tag_filter("os:Ubuntu,os:Debian")
