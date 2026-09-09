from __future__ import annotations

from typing import TYPE_CHECKING, Any, ClassVar

import frappe

if TYPE_CHECKING:
	from jwt import PyJWKClient

JWKS_ALGORITHMS = (
	"RS256",
	"RS384",
	"RS512",
	"ES256",
	"ES384",
	"ES512",
	"PS256",
	"PS384",
	"PS512",
	"EdDSA",
)


class CentralTokenValidator:
	"""Validate the tokens that the central issuer mints for the Atlas API."""

	jwks_clients: ClassVar[dict[str, PyJWKClient]] = {}

	def get_claims(self, token: str) -> dict[str, Any] | None:
		"""Return the claims of one valid token, or None when it is not usable."""
		import jwt
		from jwt import PyJWKClient

		settings = frappe.get_cached_doc("Atlas Settings")
		jwks_url = settings.central_jwks_url
		if not token or not jwks_url:
			return None

		try:
			kid = jwt.get_unverified_header(token).get("kid")
			if not isinstance(kid, str):
				return None

			# An unknown key ID must not make an attacker refetch the key set.
			signing_key = PyJWKClient.match_kid(self.get_client(jwks_url).get_signing_keys(), kid)
			if signing_key is None:
				return None

			return jwt.decode(
				token,
				signing_key.key,
				algorithms=JWKS_ALGORITHMS,
				audience=settings.admin_audience_id,
				options={"require": ["exp", "aud"], "verify_aud": True},
			)
		except jwt.PyJWTError:
			return None

	@classmethod
	def get_client(cls, jwks_url: str) -> PyJWKClient:
		"""Return the cached key set client for one issuer."""
		from jwt import PyJWKClient

		client = cls.jwks_clients.get(jwks_url)
		if client is None:
			# A real user agent. The urllib default is blocked as a bot by some issuers.
			client = PyJWKClient(jwks_url, headers={"User-Agent": "atlas"})
			cls.jwks_clients[jwks_url] = client

		return client
