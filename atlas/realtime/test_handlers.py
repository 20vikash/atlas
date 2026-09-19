import asyncio
import base64
import json
from types import SimpleNamespace
from unittest import IsolatedAsyncioTestCase
from unittest.mock import AsyncMock, Mock, patch

from atlas.realtime import handlers

CERTIFICATE = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"


class TestConsoleHandlers(IsolatedAsyncioTestCase):
	async def test_open_uses_the_regional_ca_for_the_metal_connection(self) -> None:
		socket = SimpleNamespace(sid="socket-1", site="test.local", emit=AsyncMock())
		connection = {
			"url": "wss://192.0.2.12:9000/v1/vms/vm-1/console?mode=tty",
			"authorization": "Bearer token",
			"ca_certificate": CERTIFICATE,
		}
		cache = SimpleNamespace(getdel=AsyncMock(return_value=json.dumps(connection).encode()))
		tls_context = Mock()
		metal_connection = Mock()
		session = Mock()

		with (
			patch.object(handlers, "_cache", return_value=cache),
			patch.object(handlers.ssl, "create_default_context", return_value=tls_context) as create_context,
			patch.object(handlers.websockets, "connect", AsyncMock(return_value=metal_connection)) as connect,
			patch.object(handlers, "ConsoleSession", return_value=session),
			patch.dict(handlers._sessions, {}, clear=True),
		):
			await handlers.atlas_console_open(socket, "a" * 48)
			self.assertIs(handlers._sessions[socket.sid], session)

		create_context.assert_called_once_with(cadata=connection["ca_certificate"])
		connect.assert_awaited_once_with(
			connection["url"],
			additional_headers={"Authorization": connection["authorization"]},
			max_size=None,
			ssl=tls_context,
		)
		socket.emit.assert_awaited_once_with("atlas_console_ready")

	async def test_open_rejects_an_invalid_stored_payload(self) -> None:
		socket = SimpleNamespace(sid="socket-1", site="test.local", emit=AsyncMock())
		cache = SimpleNamespace(getdel=AsyncMock(return_value=b"not-json"))

		with (
			patch.object(handlers, "_cache", return_value=cache),
			patch.object(handlers.frappe, "log_error") as log_error,
			patch.dict(handlers._sessions, {}, clear=True),
		):
			await handlers.atlas_console_open(socket, "a" * 48)

		log_error.assert_called_once_with(title="Invalid console token payload for site test.local")
		socket.emit.assert_awaited_once_with(
			"atlas_console_error", "This console link is invalid or expired."
		)

	async def test_input_rejects_invalid_base64(self) -> None:
		socket = SimpleNamespace(sid="socket-1", emit=AsyncMock())
		session = SimpleNamespace(send_input=AsyncMock())

		with patch.dict(handlers._sessions, {"socket-1": session}, clear=True):
			await handlers.atlas_console_input(socket, "not base64!")

		session.send_input.assert_not_awaited()
		socket.emit.assert_awaited_once_with("atlas_console_error", "Console input is invalid.")

	async def test_input_rejects_an_oversized_payload(self) -> None:
		socket = SimpleNamespace(sid="socket-1", emit=AsyncMock())
		session = SimpleNamespace(send_input=AsyncMock())
		data = base64.b64encode(b"a" * (handlers.MAXIMUM_CONSOLE_INPUT_BYTES + 1)).decode()

		with patch.dict(handlers._sessions, {"socket-1": session}, clear=True):
			await handlers.atlas_console_input(socket, data)

		session.send_input.assert_not_awaited()
		socket.emit.assert_awaited_once_with("atlas_console_error", "Console input is too large.")

	async def test_session_owns_idempotent_cleanup(self) -> None:
		socket = SimpleNamespace(sid="socket-1", emit=AsyncMock())
		connection = SimpleNamespace(close=AsyncMock())
		stream_task = asyncio.create_task(asyncio.Event().wait())
		session = object.__new__(handlers.ConsoleSession)
		session.socket = socket
		session.connection = connection
		session.is_closed = False
		session.stream_task = stream_task

		with patch.dict(handlers._sessions, {"socket-1": session}, clear=True):
			await session.close()
			await session.close()
			self.assertNotIn("socket-1", handlers._sessions)

		self.assertTrue(stream_task.cancelled())
		connection.close.assert_awaited_once()
		socket.emit.assert_awaited_once_with("atlas_console_closed")
