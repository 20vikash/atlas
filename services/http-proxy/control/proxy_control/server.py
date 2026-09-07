import asyncio
import socket

import uvicorn
from fastapi import FastAPI

from .config import LISTEN_ADDRESS

CONTROL_PORT = 9000

def run(app: FastAPI) -> None:
	try:
		asyncio.run(_serve(app))
	except KeyboardInterrupt:
		pass


async def _serve(app: FastAPI) -> None:
	listeners = [_listener(socket.AF_INET, LISTEN_ADDRESS, CONTROL_PORT)]
	try:
		await uvicorn.Server(uvicorn.Config(app, log_level="info")).serve(sockets=listeners)
	finally:
		for listener in listeners:
			listener.close()


def _listener(family: socket.AddressFamily, address: str, port: int) -> socket.socket:
	listener = socket.socket(family)
	listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
	if family == socket.AF_INET6:
		listener.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
	listener.bind((address, port))
	listener.listen(socket.SOMAXCONN)
	return listener
