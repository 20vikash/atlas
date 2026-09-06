import json
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.vm.core.console_token import ConsoleConnection, is_valid_console_token, issue_console_token


class TestConsoleConnection(UnitTestCase):
	def test_connection_accepts_a_websocket_url_and_authorization(self) -> None:
		connection = ConsoleConnection.from_json(
			json.dumps({"url": "wss://metal.example.test/console", "authorization": "Bearer token"})
		)

		self.assertEqual(connection.url, "wss://metal.example.test/console")
		self.assertEqual(connection.authorization, "Bearer token")

	def test_connection_rejects_an_http_url(self) -> None:
		with self.assertRaisesRegex(ValueError, "WebSocket URL"):
			ConsoleConnection.from_value(
				{"url": "https://metal.example.test/console", "authorization": "Bearer token"}
			)

	def test_console_token_requires_the_generated_format(self) -> None:
		self.assertTrue(is_valid_console_token("a" * 48))
		self.assertFalse(is_valid_console_token("short"))
		self.assertFalse(is_valid_console_token("!" * 48))

	def test_token_is_not_stored_for_an_invalid_connection(self) -> None:
		with (
			patch("atlas.vm.core.console_token.redis.from_url") as from_url,
			self.assertRaises(ValueError),
		):
			issue_console_token({"url": "not-a-url", "authorization": "Bearer token"})

		from_url.assert_not_called()

	def test_token_stores_only_validated_connection_values(self) -> None:
		client = Mock()
		with (
			patch("atlas.vm.core.console_token.frappe.generate_hash", return_value="token"),
			patch("atlas.vm.core.console_token.redis.from_url", return_value=client),
		):
			token = issue_console_token(
				{
					"url": "ws://192.0.2.1:9000/v1/vms/VM-00001/console",
					"authorization": "Bearer secret",
					"ignored": "value",
				}
			)

		self.assertEqual(token, "token")
		stored_connection = json.loads(client.set.call_args.args[1])
		self.assertEqual(
			stored_connection,
			{
				"url": "ws://192.0.2.1:9000/v1/vms/VM-00001/console",
				"authorization": "Bearer secret",
			},
		)
