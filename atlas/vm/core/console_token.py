"""Create short-lived VM console tokens."""

from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from typing import Any
from urllib.parse import urlparse

import frappe
import redis

CONSOLE_TOKEN_TTL_SECONDS = 60


@dataclass(frozen=True, slots=True)
class ConsoleConnection:
	"""Store one validated Metal console connection."""

	url: str
	authorization: str

	@classmethod
	def from_value(cls, value: object) -> ConsoleConnection:
		"""Return a validated console connection."""
		if not isinstance(value, dict):
			raise ValueError("Console connection must be an object")
		url = value.get("url")
		authorization = value.get("authorization")
		if not isinstance(url, str) or not cls.is_websocket_url(url):
			raise ValueError("Console connection has an invalid WebSocket URL")
		if (
			not isinstance(authorization, str)
			or not authorization
			or "\r" in authorization
			or "\n" in authorization
		):
			raise ValueError("Console connection has no authorization value")
		return cls(url=url, authorization=authorization)

	@classmethod
	def from_json(cls, value: str | bytes) -> ConsoleConnection:
		"""Decode and validate one stored console connection."""
		return cls.from_value(json.loads(value))

	@staticmethod
	def is_websocket_url(value: str) -> bool:
		"""Return whether a URL identifies a WebSocket server."""
		parsed = urlparse(value)
		return parsed.scheme in {"ws", "wss"} and bool(parsed.netloc)

	def as_dict(self) -> dict[str, str]:
		"""Return values that can be stored as JSON."""
		return asdict(self)


def console_token_key(site: str, token: str) -> str:
	"""Return the Redis key for one site and token."""
	return f"atlas:console:token:{site}:{token}"


def is_valid_console_token(value: object) -> bool:
	"""Return whether a value has the generated console token format."""
	return isinstance(value, str) and len(value) == 48 and value.isalnum()


def issue_console_token(connection: dict[str, Any]) -> str:
	"""Store the console connection under a new token and return the token."""
	validated_connection = ConsoleConnection.from_value(connection)
	token = frappe.generate_hash(length=48)
	client = redis.from_url(frappe.conf.redis_cache)
	client.set(
		console_token_key(frappe.local.site, token),
		json.dumps(validated_connection.as_dict()),
		ex=CONSOLE_TOKEN_TTL_SECONDS,
	)
	return token
