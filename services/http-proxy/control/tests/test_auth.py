import json
import time
from pathlib import Path

import bcrypt
import jwt
import pytest
from cryptography.hazmat.primitives.asymmetric import rsa
from fastapi import HTTPException
from jwt import PyJWK
from jwt.algorithms import RSAAlgorithm

from proxy_control.auth import Authentication

PASSWORD = "correct-horse"
AUDIENCE = "atlas-proxy-control"


def write_config(path: Path, password: str = "", jwks: bool = False) -> Path:
	"""Write a configuration file with the credentials a test needs."""
	lines = ["[tls]", 'wildcard_domain = "*.par-1.example.com"', 'fullchain_pem = "leaf"', 'private_key_pem = "key"', "", "[auth]"]
	if password:
		lines.append(f'password_hash = "{bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()}"')
	if jwks:
		lines.append('jwks_url = "https://issuer.example.com/jwks.json"')
		lines.append(f'jwks_audience_id = "{AUDIENCE}"')

	path.write_text("\n".join(lines) + "\n")
	return path


@pytest.fixture
def config_file(tmp_path: Path) -> Path:
	return write_config(tmp_path / "proxy-control.toml", password=PASSWORD)


def test_require_accepts_correct_password(config_file):
	Authentication(config_file).require(authorization=f"Bearer {PASSWORD}")


def test_require_rejects_wrong_password(config_file):
	with pytest.raises(HTTPException):
		Authentication(config_file).require(authorization="Bearer wrong-password")


def test_require_rejects_missing_bearer_scheme(config_file):
	with pytest.raises(HTTPException):
		Authentication(config_file).require(authorization=PASSWORD)


def test_require_rejects_missing_header(config_file):
	with pytest.raises(HTTPException):
		Authentication(config_file).require(authorization=None)


# A broken or missing file authorizes nobody instead of failing open.
def test_require_rejects_missing_config_file(tmp_path):
	with pytest.raises(HTTPException):
		Authentication(tmp_path / "missing.toml").require(authorization=f"Bearer {PASSWORD}")


def test_require_rejects_a_malformed_config_file(tmp_path):
	path = tmp_path / "proxy-control.toml"
	path.write_text("[auth\n")

	with pytest.raises(HTTPException):
		Authentication(path).require(authorization=f"Bearer {PASSWORD}")


# An empty configuration authorizes nobody.
def test_require_rejects_file_with_no_credentials(tmp_path):
	path = tmp_path / "proxy-control.toml"
	path.write_text("")

	with pytest.raises(HTTPException):
		Authentication(path).require(authorization=f"Bearer {PASSWORD}")


# Atlas pushes a new credential without restarting the daemon.
def test_require_reads_the_credential_on_each_request(tmp_path):
	path = write_config(tmp_path / "proxy-control.toml", password=PASSWORD)
	auth = Authentication(path)
	auth.require(authorization=f"Bearer {PASSWORD}")

	write_config(path, password="rotated")

	auth.require(authorization="Bearer rotated")
	with pytest.raises(HTTPException):
		auth.require(authorization=f"Bearer {PASSWORD}")


class _FakeJWKClient:
	def __init__(self, keys: list[PyJWK]):
		self._keys = keys

	def get_signing_keys(self) -> list[PyJWK]:
		return self._keys


def _key_pair() -> rsa.RSAPrivateKey:
	return rsa.generate_private_key(public_exponent=65537, key_size=2048)


def _jwk(public_key, kid: str) -> PyJWK:
	jwk_data = json.loads(RSAAlgorithm.to_jwk(public_key))
	jwk_data["kid"] = kid
	return PyJWK.from_json(json.dumps(jwk_data))


def _token(private_key, kid: str, audience: str = AUDIENCE, ttl: int = 3600) -> str:
	payload = {"sub": "controller", "aud": audience, "exp": int(time.time()) + ttl}
	return jwt.encode(payload, private_key, algorithm="RS256", headers={"kid": kid})


def _jwks_auth(path: Path, jwk: PyJWK, password: str = PASSWORD) -> Authentication:
	write_config(path, password=password, jwks=True)
	auth = Authentication(path)
	auth._jwks_client = _FakeJWKClient([jwk])
	auth._jwks_url = "https://issuer.example.com/jwks.json"
	return auth


def test_require_accepts_valid_jwks_token(config_file):
	private_key = _key_pair()
	jwk = _jwk(private_key.public_key(), kid="key-1")
	auth = _jwks_auth(config_file, jwk)
	token = _token(private_key, kid="key-1")
	auth.require(authorization=f"Bearer {token}")


# A fresh install has no password. JWKS must still give access.
def test_require_accepts_valid_jwks_token_without_a_password(tmp_path):
	private_key = _key_pair()
	jwk = _jwk(private_key.public_key(), kid="key-1")
	auth = _jwks_auth(tmp_path / "proxy-control.toml", jwk, password="")
	token = _token(private_key, kid="key-1")
	auth.require(authorization=f"Bearer {token}")


def test_require_rejects_jwks_token_with_wrong_audience(config_file):
	private_key = _key_pair()
	jwk = _jwk(private_key.public_key(), kid="key-1")
	auth = _jwks_auth(config_file, jwk)
	token = _token(private_key, kid="key-1", audience="someone-else")
	with pytest.raises(HTTPException):
		auth.require(authorization=f"Bearer {token}")


def test_require_rejects_jwks_token_with_unknown_kid(config_file):
	private_key = _key_pair()
	jwk = _jwk(private_key.public_key(), kid="key-1")
	auth = _jwks_auth(config_file, jwk)
	token = _token(private_key, kid="key-does-not-exist")
	with pytest.raises(HTTPException):
		auth.require(authorization=f"Bearer {token}")


def test_require_rejects_expired_jwks_token(config_file):
	private_key = _key_pair()
	jwk = _jwk(private_key.public_key(), kid="key-1")
	auth = _jwks_auth(config_file, jwk)
	token = _token(private_key, kid="key-1", ttl=-60)
	with pytest.raises(HTTPException):
		auth.require(authorization=f"Bearer {token}")


def test_jwks_is_ignored_when_not_configured(config_file):
	private_key = _key_pair()
	token = _token(private_key, kid="key-1")
	with pytest.raises(HTTPException):
		Authentication(config_file).require(authorization=f"Bearer {token}")
