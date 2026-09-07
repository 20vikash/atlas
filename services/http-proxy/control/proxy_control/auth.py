from pathlib import Path
from typing import Annotated, ClassVar

import bcrypt
import jwt
from fastapi import Depends, Header, HTTPException, status
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from jwt import PyJWKClient

from .config import AuthConfig, ConfigError, load

CONTROL_BEARER_SCHEME = "BearerAuth"
bearer = HTTPBearer(
	scheme_name=CONTROL_BEARER_SCHEME,
	bearerFormat="password or JWT",
	description="Use the control API password or a valid JWT.",
	auto_error=False,
)


class Authentication:
	"""Authenticate bearer passwords and JWTs."""

	_JWKS_ALGORITHMS: ClassVar[tuple[str, ...]] = (
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

	def __init__(self, path: Path | None = None) -> None:
		self.path = path
		self._jwks_client: PyJWKClient | None = None
		self._jwks_url = ""

	def require(self, authorization: Annotated[str | None, Header()] = None) -> None:
		scheme, _, token = (authorization or "").partition(" ")
		if scheme.lower() != "bearer" or not token:
			raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="unauthorized")

		auth = self._auth()
		if self._matches_password(auth, token) or self._matches_jwks(auth, token):
			return

		raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="unauthorized")

	def require_request(
		self, credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(bearer)]
	) -> None:
		"""Authenticate one API request."""
		authorization = f"{credentials.scheme} {credentials.credentials}" if credentials else None
		self.require(authorization)

	def _auth(self) -> AuthConfig:
		"""Return the current credentials."""
		try:
			return load(self.path).auth
		except ConfigError:
			return AuthConfig()

	def _matches_password(self, auth: AuthConfig, password: str) -> bool:
		if not auth.password_hash:
			return False
		try:
			return bcrypt.checkpw(password.encode(), auth.password_hash.encode())
		except ValueError:
			return False

	def _matches_jwks(self, auth: AuthConfig, token: str) -> bool:
		if not auth.jwks_url or not auth.jwks_audience_id:
			return False
		try:
			kid = jwt.get_unverified_header(token).get("kid")
			if not isinstance(kid, str):
				return False

			signing_key = PyJWKClient.match_kid(self._jwks_client_instance(auth).get_signing_keys(), kid)
			if signing_key is None:
				return False

			jwt.decode(
				token,
				signing_key.key,
				algorithms=self._JWKS_ALGORITHMS,
				audience=auth.jwks_audience_id,
				options={"require": ["exp", "aud"], "verify_aud": True},
			)
			return True
		except jwt.PyJWTError:
			return False

	def _jwks_client_instance(self, auth: AuthConfig) -> PyJWKClient:
		"""Return a JWKS client for the configured URL."""
		if self._jwks_client is None or self._jwks_url != auth.jwks_url:
			self._jwks_client = PyJWKClient(auth.jwks_url, headers={"User-Agent": "atlas-proxy-control"})
			self._jwks_url = auth.jwks_url

		return self._jwks_client
