import asyncio
import base64
from types import SimpleNamespace
from unittest import IsolatedAsyncioTestCase
from unittest.mock import AsyncMock, patch

from atlas.realtime import handlers


class TestConsoleHandlers(IsolatedAsyncioTestCase):
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
