from typing import Any

from fastapi import HTTPException

from .client import ProxyClient


class MappingStore:
	"""Manage OpenResty maps."""

	def __init__(self, client: ProxyClient, reserved_subdomain: str = ""):
		self.client = client
		self.reserved_subdomain = reserved_subdomain

	async def get(self, kind: str) -> dict[str, str]:
		self._validate_kind(kind)
		status, body = await self.client.request("GET", f"/v1/{kind}")
		self._check_response(status, body)
		if not self._is_map(body):
			raise HTTPException(status_code=502, detail="proxy returned an invalid map")
		return body

	async def replace(self, kind: str, values: dict[str, str]) -> dict[str, Any]:
		"""Replace one complete map."""
		self._validate_kind(kind)
		return await self._forward("PUT", f"/v1/{kind}", self._without_reserved(kind, values))

	async def update(self, kind: str, key: str, address: str) -> dict[str, Any]:
		self._validate_kind(kind)
		self._validate_key(kind, key)
		return await self._forward("PATCH", f"/v1/{kind}/{key}", {"address": address})

	async def delete(self, kind: str, key: str) -> None:
		self._validate_kind(kind)
		self._validate_key(kind, key)
		await self._forward("DELETE", f"/v1/{kind}/{key}")

	def is_reserved(self, kind: str, key: str) -> bool:
		"""Report whether a key is the control subdomain."""
		return bool(self.reserved_subdomain) and kind == "sites" and key.lower() == self.reserved_subdomain

	def _validate_key(self, kind: str, key: str) -> None:
		if self.is_reserved(kind, key):
			raise HTTPException(status_code=409, detail=f"{key} is reserved for the proxy control daemon")

	def _without_reserved(self, kind: str, values: dict[str, str]) -> dict[str, str]:
		return {key: value for key, value in values.items() if not self.is_reserved(kind, key)}

	async def _forward(self, method: str, path: str, body: Any = None) -> dict[str, Any]:
		status, response_body = await self.client.request(method, path, body)
		self._check_response(status, response_body)
		return response_body if isinstance(response_body, dict) else {}

	def _validate_kind(self, kind: str) -> None:
		if kind not in {"sites", "domains"}:
			raise HTTPException(status_code=404, detail="mapping type not found")

	def _is_map(self, body: Any) -> bool:
		return isinstance(body, dict) and all(
			isinstance(key, str) and isinstance(address, str) for key, address in body.items()
		)

	def _check_response(self, status: int, body: Any) -> None:
		if status >= 300:
			raise HTTPException(
				status_code=502,
				detail={"proxy_status": status, "proxy": body},
			)
