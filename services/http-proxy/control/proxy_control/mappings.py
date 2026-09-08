from typing import Any

from fastapi import HTTPException

from .client import ProxyClient


class MappingStore:
	"""Manage OpenResty maps."""

	def __init__(
		self,
		client: ProxyClient,
		reserved_subdomains: tuple[str, ...] | str = (),
		wildcard_domain: str = "",
	):
		self.client = client
		if isinstance(reserved_subdomains, str):
			reserved_subdomains = (reserved_subdomains,) if reserved_subdomains else ()
		self.reserved_subdomains = {value.lower() for value in reserved_subdomains}
		self.wildcard_zone = wildcard_domain.lower().removeprefix("*.").rstrip(".")

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
		if kind != "sites":
			return False
		name = key.lower()
		return name == "proxy" or name.startswith("proxy-") or name in self.reserved_subdomains

	def without_reserved(self, kind: str, values: dict[str, str]) -> dict[str, str]:
		"""Return a map without control subdomains."""
		return self._without_reserved(kind, values)

	def _validate_key(self, kind: str, key: str) -> None:
		if self.is_reserved(kind, key):
			raise HTTPException(status_code=409, detail=f"{key} is reserved for the proxy control daemon")

		if kind == "domains" and key.startswith("*"):
			raise HTTPException(status_code=422, detail="custom-domain wildcard routes are not supported")

		if kind == "domains" and self._is_wildcard_subdomain(key):
			raise HTTPException(status_code=409, detail=f"{key} belongs in the site map")

	def _without_reserved(self, kind: str, values: dict[str, str]) -> dict[str, str]:
		for key in values:
			self._validate_key(kind, key)
		return values

	def _is_wildcard_subdomain(self, key: str) -> bool:
		if not self.wildcard_zone:
			return False
		name = key.lower().rstrip(".")
		return name == self.wildcard_zone or name.endswith(f".{self.wildcard_zone}")

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
