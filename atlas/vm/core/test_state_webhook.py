from contextlib import nullcontext
from unittest.mock import Mock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.vm.core import state_webhook


class TestStateWebhookConfiguration(UnitTestCase):
	def _configure(self, existing: bool, region_name: str | None = "par-2", **overrides):
		"""Run the configuration against a stubbed Webhook store."""
		saved: list[Mock] = []

		def build(*_arguments) -> Mock:
			document = Mock()
			saved.append(document)
			return document

		with (
			patch.object(state_webhook.frappe, "get_cached_value", return_value=region_name),
			patch.object(
				state_webhook.frappe.db,
				"advisory_lock",
				return_value=nullcontext(),
			) as configuration_lock,
			patch.object(state_webhook.frappe.db, "commit") as commit,
			patch.object(state_webhook.frappe.db, "exists", return_value=existing),
			patch.object(state_webhook.frappe, "get_doc", side_effect=build) as get_doc,
			patch.object(state_webhook.frappe, "new_doc", side_effect=build),
		):
			names = state_webhook.configure_state_webhooks(
				request_url="https://central.example.com/receive",
				webhook_secret="a-shared-secret",
				**overrides,
			)

		return names, saved, get_doc, configuration_lock, commit

	def test_configuration_creates_one_webhook_for_each_event(self) -> None:
		names, saved, _, configuration_lock, commit = self._configure(existing=False)

		self.assertEqual(len(saved), 2)
		self.assertEqual(
			names,
			[
				"Virtual Machine State - On Update - Central - 1",
				"Virtual Machine State - On Trash - Central - 1",
			],
		)
		self.assertEqual([document.name for document in saved], names)
		self.assertEqual([document.webhook_docevent for document in saved], ["on_update", "on_trash"])
		configuration_lock.assert_called_once_with("configure_state_webhooks")
		commit.assert_not_called()
		for document in saved:
			document.save.assert_called_once_with(ignore_permissions=True)
			self.assertEqual(document.webhook_doctype, "Virtual Machine State")
			self.assertEqual(document.enable_security, 1)
			self.assertEqual(document.webhook_secret, "a-shared-secret")
			self.assertEqual(document.enabled, 1)
			document.set.assert_called_once_with(
				"webhook_headers",
				[
					{"key": "Content-Type", "value": "application/json"},
					{"key": "X-Atlas-Region", "value": "par-2"},
				],
			)

	def test_configuration_updates_a_webhook_that_exists(self) -> None:
		names, saved, get_doc, configuration_lock, commit = self._configure(
			existing=True,
			central_id=4,
			enabled=False,
		)

		self.assertEqual(names[0], "Virtual Machine State - On Update - Central - 4")
		self.assertEqual(get_doc.call_count, 2)
		self.assertEqual([document.webhook_doctype for document in saved], ["Virtual Machine State"] * 2)
		self.assertEqual([document.webhook_docevent for document in saved], ["on_update", "on_trash"])
		self.assertEqual([document.condition for document in saved], [None, None])
		self.assertEqual([document.is_dynamic_url for document in saved], [0, 0])
		self.assertEqual([document.background_jobs_queue for document in saved], [None, None])
		self.assertEqual([document.enabled for document in saved], [0, 0])
		configuration_lock.assert_called_once_with("configure_state_webhooks")
		commit.assert_not_called()

	def test_the_trash_body_reports_the_removal(self) -> None:
		_, saved, _, _, _ = self._configure(existing=False)
		body = frappe.parse_json(saved[1].webhook_json)

		self.assertEqual(body["event"], "vm.state.deleted")
		self.assertEqual(body["virtual_machine"], "{{ doc.virtual_machine }}")

	def test_the_headers_omit_an_unset_region(self) -> None:
		_, saved, _, _, _ = self._configure(existing=False, region_name=None)

		for document in saved:
			document.set.assert_called_once_with(
				"webhook_headers",
				[{"key": "Content-Type", "value": "application/json"}],
			)
