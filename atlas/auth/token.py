from __future__ import annotations

from typing import Any

import frappe
import jwt
from jwt import PyJWK

from atlas.auth.jwks import CENTRAL_ISSUER, issuer_for_key_id, merged_jwks


class TokenValidator:
	"""Validate issuer-bound tokens for the Atlas API."""

	def claims(self, token: str) -> dict[str, Any] | None:
		"""Return valid Atlas API claims, or None when the token is not usable."""
		settings = frappe.get_cached_doc("Atlas Settings")
		if not token:
			return None

		try:
			header = jwt.get_unverified_header(token)
			key_id = header.get("kid")
			if not isinstance(key_id, str) or header.get("alg") != "EdDSA":
				return None

			issuer = issuer_for_key_id(key_id, settings.region_id)
			key = self._signing_key(key_id)
			if issuer is None or key is None:
				return None

			claims = jwt.decode(
				token,
				key.key,
				algorithms=["EdDSA"],
				audience=settings.admin_audience_id,
				issuer=issuer,
				options={
					"require": ["iss", "sub", "aud", "scope", "tenant", "iat", "exp"],
					"verify_aud": True,
				},
			)
			if claims.get("aud") != settings.admin_audience_id:
				return None
			return claims if self._has_atlas_authority(claims, issuer) else None
		except jwt.PyJWTError, ValueError, TypeError:
			return None

	def _signing_key(self, key_id: str) -> PyJWK | None:
		for key in merged_jwks()["keys"]:
			if key.get("kid") == key_id:
				return PyJWK.from_dict(key)
		return None

	def _has_atlas_authority(self, claims: dict[str, Any], issuer: str) -> bool:
		subject = claims.get("sub")
		if not isinstance(subject, str) or not subject:
			return False
		if claims.get("scope") != "*":
			return False
		if claims.get("constraints", {}) != {}:
			return False
		if not all(_is_timestamp(claims.get(name)) for name in ("iat", "exp")):
			return False
		if "nbf" in claims and not _is_timestamp(claims["nbf"]):
			return False

		tenant = claims.get("tenant")
		if issuer == CENTRAL_ISSUER:
			return subject == CENTRAL_ISSUER and tenant == "*"

		return subject == "cargo" and tenant == "1"


def _is_timestamp(value: Any) -> bool:
	return isinstance(value, int | float) and not isinstance(value, bool)
