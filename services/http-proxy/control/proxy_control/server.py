import asyncio
import os
import socket

import uvicorn
from fastapi import FastAPI

from .config import LISTEN_ADDRESS

CONTROL_PORT = 9000
SYSTEMD_FIRST_FD = 3


def run(app: FastAPI) -> None:
	try:
		asyncio.run(_serve(app))
	except KeyboardInterrupt:
		pass


async def _serve(app: FastAPI) -> None:
	listeners = get_inherited_listeners() or [_listener(socket.AF_INET, LISTEN_ADDRESS, CONTROL_PORT)]
	try:
		await uvicorn.Server(uvicorn.Config(app, log_level="info")).serve(sockets=listeners)
	finally:
		for listener in listeners:
			listener.close()


def get_inherited_listeners() -> list[socket.socket]:
	"""Return systemd listener sockets."""
	if os.environ.get("LISTEN_PID") != str(os.getpid()):
		return []

	count = int(os.environ.get("LISTEN_FDS", "0"))
	return [socket.socket(fileno=SYSTEMD_FIRST_FD + index) for index in range(count)]


def _listener(family: socket.AddressFamily, address: str, port: int) -> socket.socket:
	listener = socket.socket(family)
	listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
	if family == socket.AF_INET6:
		listener.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
	listener.bind((address, port))
	listener.listen(socket.SOMAXCONN)
	return listener
