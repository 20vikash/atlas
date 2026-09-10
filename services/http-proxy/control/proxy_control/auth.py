import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Annotated

import bcrypt
import jwt
from fastapi import Depends, HTTPException, status
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from jwt import PyJWKClient

from .config import AuthConfig, ConfigError, load

CONTROL_BEARER_SCHEME = "BearerAuth"
KNOWN_SCOPES = frozenset({"*", "site:*"})
bearer = HTTPBearer(
	scheme_name=CONTROL_BEARER_SCHEME,
	bearerFormat="password or JWT",
	description="Use the regional proxy password or a valid JWT.",
	auto_error=False,
)


@dataclass(frozen=True)
class Authorization:
	"""The verified Proxy authority for one request."""

	scopes: frozenset[str]
	constraints: dict[str, dict[str, str]] = field(default_factory=dict)

	def require(self, resource: str, action: str, name: str | None = None) -> None:
		"""Refuse a request outside this authority."""
		if not self._has_scope(resource, action) or not self._matches_name(resource, name):
			raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="forbidden")

	def require_unconstrained(self, resource: str, action: str) -> None:
		"""Refuse a full-map operation when the authority has a resource constraint."""
		self.require(resource, action)
		if resource in self.constraints:
			raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="forbidden")

	def filter(self, resource: str, values: dict[str, str]) -> dict[str, str]:
		"""Return only the map values that this authority can read."""
		self.require(resource, "read")
		return {name: address for name, address in values.items() if self._matches_name(resource, name)}

	def _has_scope(self, resource: str, action: str) -> bool:
		return bool(self.scopes & {"*", f"{resource}:*", f"{resource}:{action}"})

	def _matches_name(self, resource: str, name: str | None) -> bool:
		constraint = self.constraints.get(resource)
		if constraint is None:
			return True
		if name is None:
			return True
		return name.endswith(constraint["suffix"])


class Authentication:
	"""Authenticate bearer passwords and issuer-bound JWTs."""

	def __init__(self, path: Path | None = None) -> None:
		self.path = path
		self._jwks_client: PyJWKClient | None = None
		self._jwks_url = ""

	def require(self, authorization: str | None = None) -> Authorization:
		scheme, _, token = (authorization or "").partition(" ")
		if scheme.lower() != "bearer" or not token:
			raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="unauthorized")

		auth = self._auth()
		if self._matches_password(auth, token):
			return Authorization(frozenset({"*"}))

		verified = self._jwt_authorization(auth, token)
		if verified is not None:
			return verified

		raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="unauthorized")

	def require_request(
		self, credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(bearer)]
	) -> Authorization:
		"""Authenticate one API request."""
		authorization = f"{credentials.scheme} {credentials.credentials}" if credentials else None
		return self.require(authorization)

	def _auth(self) -> AuthConfig:
		"""Return the current credentials."""
		try:
			return load(self.path).auth
		except ConfigError:
			return AuthConfig()

	def _matches_password(self, auth: AuthConfig, password: str) -> bool:
		password_hashes = [auth.password_hash]
		if time.time() <= auth.previous_password_valid_until:
			password_hashes.append(auth.previous_password_hash)

		for password_hash in password_hashes:
			if not password_hash:
				continue
			try:
				if bcrypt.checkpw(password.encode(), password_hash.encode()):
					return True
			except ValueError:
				continue

		return False

	def _jwt_authorization(self, auth: AuthConfig, token: str) -> Authorization | None:
		if not auth.jwks_url or not auth.jwks_audience_id or not auth.jwks_issuers:
			return None
		try:
			header = jwt.get_unverified_header(token)
			key_id = header.get("kid")
			if not isinstance(key_id, str) or header.get("alg") != "EdDSA":
				return None

			issuer = _issuer_for_key_id(key_id, auth.jwks_issuers)
			if issuer is None:
				return None

			signing_key = PyJWKClient.match_kid(self._jwks_client_instance(auth).get_signing_keys(), key_id)
			if signing_key is None or signing_key.algorithm_name != "EdDSA":
				return None

			claims = jwt.decode(
				token,
				signing_key.key,
				algorithms=["EdDSA"],
				audience=auth.jwks_audience_id,
				issuer=issuer,
				options={"require": ["iss", "sub", "aud", "scope", "iat", "exp"]},
			)
			if claims.get("aud") != auth.jwks_audience_id:
				return None
			return _authorization_from_claims(claims, issuer)
		except jwt.PyJWTError, ValueError, TypeError:
			return None

	def _jwks_client_instance(self, auth: AuthConfig) -> PyJWKClient:
		"""Return a JWKS client for the configured URL."""
		if self._jwks_client is None or self._jwks_url != auth.jwks_url:
			self._jwks_client = PyJWKClient(auth.jwks_url, headers={"User-Agent": "atlas-proxy-control"})
			self._jwks_url = auth.jwks_url

		return self._jwks_client


def _issuer_for_key_id(key_id: str, issuers: tuple[str, ...]) -> str | None:
	for issuer in issuers:
		if key_id.startswith(f"{issuer}:"):
			return issuer
	return None


def _authorization_from_claims(claims: dict[str, object], issuer: str) -> Authorization | None:
	subject = claims.get("sub")
	scope = claims.get("scope")
	if not isinstance(subject, str) or not subject or not isinstance(scope, str):
		return None
	if "tenant" in claims:
		return None
	if not all(_is_timestamp(claims.get(name)) for name in ("iat", "exp")):
		return None
	if "nbf" in claims and not _is_timestamp(claims["nbf"]):
		return None

	scopes = frozenset(scope.split())
	if not scopes or not scopes <= KNOWN_SCOPES:
		return None

	constraints = claims.get("constraints", {})
	if not isinstance(constraints, dict) or set(constraints) - {"site"}:
		return None
	if issuer == "central":
		if subject != "central" or scopes != {"*"} or constraints:
			return None
		return Authorization(scopes)

	if subject != "cargo" or scopes != {"site:*"}:
		return None
	if not constraints:
		return Authorization(scopes)

	site = constraints["site"]
	if not isinstance(site, dict) or set(site) != {"suffix"}:
		return None
	suffix = site["suffix"]
	if not isinstance(suffix, str) or not suffix:
		return None

	return Authorization(scopes, {"site": {"suffix": suffix}})


def _is_timestamp(value: object) -> bool:
	return isinstance(value, int | float) and not isinstance(value, bool)
