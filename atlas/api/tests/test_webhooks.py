from unittest.mock import patch

import frappe
from frappe.tests import UnitTestCase

from atlas.api.routes.webhooks import configure_webhooks
from atlas.api.tests.test_support import TENANT_ID, api_request, call_route, error_body

REQUEST_URL = "https://central.example.com/api/method/central.api.state_delivery.receive"
BODY = {"request_url": REQUEST_URL, "webhook_secret": "a-shared-secret"}


class TestConfigureWebhooks(UnitTestCase):
	def _configure(
		self,
		body: dict[str, object],
		tenant_id: int | None = None,
		*,
		developer_mode: int = 0,
		allow_multiple_central_webhooks: int = 0,
	):
		"""Run the configuration route for one caller."""
		with (
			patch.dict(
				frappe.conf,
				{
					"developer_mode": developer_mode,
					"allow_multiple_central_webhooks": allow_multiple_central_webhooks,
				},
			),
			api_request("PUT", "/api/atlas/webhooks", tenant_id=tenant_id, json=body),
			patch(
				"atlas.api.routes.webhooks.configure_state_webhooks",
				return_value=["Virtual Machine State - On Update - Central - 1"],
			) as configure,
		):
			return call_route(configure_webhooks), configure

	def test_configuration_configures_the_deliveries(self) -> None:
		(status, body), configure = self._configure(BODY)

		self.assertEqual(status, 200)
		self.assertEqual(body["central_id"], 1)
		self.assertTrue(body["enabled"])
		self.assertEqual(body["webhooks"], ["Virtual Machine State - On Update - Central - 1"])
		configure.assert_called_once_with(
			request_url=REQUEST_URL,
			webhook_secret="a-shared-secret",
			central_id=1,
			enabled=True,
		)

	def test_configuration_carries_the_named_central_and_disabled_state(self) -> None:
		(status, _), configure = self._configure(
			{**BODY, "central_id": 4, "enabled": False},
			allow_multiple_central_webhooks=1,
		)

		self.assertEqual(status, 200)
		self.assertEqual(configure.call_args.kwargs["central_id"], 4)
		self.assertFalse(configure.call_args.kwargs["enabled"])

	def test_configuration_rejects_another_central_in_production(self) -> None:
		(status, body), configure = self._configure({**BODY, "central_id": 4})

		self.assertEqual(status, 400)
		self.assertEqual(body["error"]["code"], "invalid_request")
		configure.assert_not_called()

	def test_developer_mode_allows_another_central(self) -> None:
		(status, _), configure = self._configure({**BODY, "central_id": 4}, developer_mode=1)

		self.assertEqual(status, 200)
		self.assertEqual(configure.call_args.kwargs["central_id"], 4)

	def test_a_tenant_token_cannot_configure_the_deliveries(self) -> None:
		with (
			api_request("PUT", "/api/atlas/webhooks", tenant_id=TENANT_ID, json=BODY),
			patch("atlas.api.routes.webhooks.configure_state_webhooks") as configure,
		):
			status, body = error_body(configure_webhooks)

		self.assertEqual(status, 403)
		self.assertEqual(body["error"]["code"], "permission_denied")
		configure.assert_not_called()

	def test_configuration_rejects_a_missing_secret(self) -> None:
		(status, body), configure = self._configure({"request_url": REQUEST_URL})

		self.assertEqual(status, 400)
		self.assertEqual(body["error"]["code"], "invalid_request")
		configure.assert_not_called()

	def test_configuration_rejects_a_non_http_url(self) -> None:
		(status, body), configure = self._configure({**BODY, "request_url": "ftp://central.example.com"})

		self.assertEqual(status, 400)
		self.assertEqual(body["error"]["code"], "invalid_request")
		configure.assert_not_called()
