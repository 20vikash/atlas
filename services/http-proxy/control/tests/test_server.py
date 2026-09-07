import os
import socket

from proxy_control import server


def test_listeners_are_inherited_when_systemd_passes_them(monkeypatch) -> None:
	listener = socket.socket()
	listener.bind(("127.0.0.1", 0))
	listener.listen(1)
	monkeypatch.setattr(server, "SYSTEMD_FIRST_FD", listener.fileno())
	monkeypatch.setenv("LISTEN_PID", str(os.getpid()))
	monkeypatch.setenv("LISTEN_FDS", "1")

	inherited = server.get_inherited_listeners()

	try:
		assert [item.getsockname() for item in inherited] == [listener.getsockname()]
	finally:
		for item in inherited:
			item.detach()
		listener.close()


def test_no_listener_is_inherited_without_systemd(monkeypatch) -> None:
	monkeypatch.delenv("LISTEN_PID", raising=False)
	monkeypatch.delenv("LISTEN_FDS", raising=False)

	assert server.get_inherited_listeners() == []


def test_no_listener_is_inherited_for_another_process(monkeypatch) -> None:
	monkeypatch.setenv("LISTEN_PID", str(os.getpid() + 1))
	monkeypatch.setenv("LISTEN_FDS", "1")

	assert server.get_inherited_listeners() == []
