import json
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.vm.core.console_token import (
	CONSOLE_TOKEN_ATTEMPTS,
	CONSOLE_TOKEN_LENGTH,
	ConsoleConnection,
	is_valid_console_token,
	issue_console_token,
)


class TestConsoleConnection(UnitTestCase):
	def test_connection_accepts_a_websocket_url_and_authorization(self) -> None:
		connection = ConsoleConnection.from_json(json.dumps({"url": "wss://metal.example.test/console"}))

		self.assertEqual(connection.url, "wss://metal.example.test/console")

	def test_connection_rejects_an_http_url(self) -> None:
		with self.assertRaisesRegex(ValueError, "WebSocket URL"):
			ConsoleConnection.from_value({"url": "https://metal.example.test/console"})

	def test_console_token_requires_the_generated_format(self) -> None:
		self.assertTrue(is_valid_console_token("a" * CONSOLE_TOKEN_LENGTH))
		self.assertFalse(is_valid_console_token("short"))
		self.assertFalse(is_valid_console_token("!" * CONSOLE_TOKEN_LENGTH))

	def test_token_is_not_stored_for_an_invalid_connection(self) -> None:
		with (
			patch("atlas.vm.core.console_token.redis.from_url") as from_url,
			self.assertRaises(ValueError),
		):
			issue_console_token({"url": "not-a-url"})

		from_url.assert_not_called()

	def test_token_stores_only_validated_connection_values(self) -> None:
		client = Mock()
		with (
			patch("atlas.vm.core.console_token.frappe.generate_hash", return_value="token"),
			patch("atlas.vm.core.console_token.redis.from_url", return_value=client),
		):
			token = issue_console_token(
				{"url": "wss://192.0.2.1:9000/v1/vms/VM-00001/console", "ignored": "value"}
			)

		self.assertEqual(token, "token")
		stored_connection = json.loads(client.set.call_args.args[1])
		self.assertEqual(stored_connection, {"url": "wss://192.0.2.1:9000/v1/vms/VM-00001/console"})

	def test_token_is_generated_again_when_its_key_is_taken(self) -> None:
		client = Mock()
		client.set.side_effect = [None, True]
		with (
			patch("atlas.vm.core.console_token.frappe.generate_hash", side_effect=["taken", "free"]),
			patch("atlas.vm.core.console_token.redis.from_url", return_value=client),
		):
			token = issue_console_token({"url": "wss://metal.example.test/console"})

		self.assertEqual(token, "free")
		self.assertTrue(client.set.call_args.kwargs["nx"])

	def test_token_is_not_issued_when_every_key_is_taken(self) -> None:
		client = Mock()
		client.set.return_value = None
		with (
			patch("atlas.vm.core.console_token.redis.from_url", return_value=client),
			self.assertRaisesRegex(RuntimeError, "Could not generate a console token"),
		):
			issue_console_token({"url": "wss://metal.example.test/console"})

		self.assertEqual(client.set.call_count, CONSOLE_TOKEN_ATTEMPTS)
