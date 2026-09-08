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

AUDIENCE = "atlas-proxy-control"
PASSWORD = "correct-horse"
PREVIOUS_PASSWORD = "previous-horse"


def write_config(
	path: Path,
	has_jwks: bool = True,
	password: str = "",
	previous_password: str = "",
	previous_password_valid_until: int = 0,
) -> Path:
	"""Write a configuration with the credentials that a test needs."""
	lines = [
		"[tls]",
		'wildcard_domain = "*.par-1.example.com"',
		'fullchain_pem = "leaf"',
		'private_key_pem = "key"',
	]
	if has_jwks or password or previous_password:
		lines.extend(["", "[auth]"])
	if password:
		lines.append(f'password_hash = "{bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()}"')
	if previous_password:
		lines.extend(
			[
				f'previous_password_hash = "{bcrypt.hashpw(previous_password.encode(), bcrypt.gensalt()).decode()}"',
				f"previous_password_valid_until = {previous_password_valid_until}",
			]
		)
	if has_jwks:
		lines.extend(
			[
				'jwks_url = "https://issuer.example.com/jwks.json"',
				f'jwks_audience_id = "{AUDIENCE}"',
			]
		)
	path.write_text("\n".join(lines) + "\n")
	return path


def test_require_accepts_the_current_password(tmp_path):
	path = write_config(tmp_path / "proxy-control.toml", has_jwks=False, password=PASSWORD)

	Authentication(path).require(f"Bearer {PASSWORD}")


def test_require_accepts_the_previous_password_before_expiry(tmp_path):
	path = write_config(
		tmp_path / "proxy-control.toml",
		has_jwks=False,
		password=PASSWORD,
		previous_password=PREVIOUS_PASSWORD,
		previous_password_valid_until=int(time.time()) + 600,
	)

	Authentication(path).require(f"Bearer {PREVIOUS_PASSWORD}")


def test_require_rejects_the_previous_password_after_expiry(tmp_path):
	path = write_config(
		tmp_path / "proxy-control.toml",
		has_jwks=False,
		password=PASSWORD,
		previous_password=PREVIOUS_PASSWORD,
		previous_password_valid_until=int(time.time()) - 1,
	)

	with pytest.raises(HTTPException):
		Authentication(path).require(f"Bearer {PREVIOUS_PASSWORD}")


def test_require_rejects_an_incorrect_password(tmp_path):
	path = write_config(tmp_path / "proxy-control.toml", has_jwks=False, password=PASSWORD)

	with pytest.raises(HTTPException):
		Authentication(path).require("Bearer incorrect-password")


class _FakeJWKClient:
	def __init__(self, keys: list[PyJWK]):
		self.keys = keys

	def get_signing_keys(self) -> list[PyJWK]:
		return self.keys


def _key_pair() -> rsa.RSAPrivateKey:
	return rsa.generate_private_key(public_exponent=65537, key_size=2048)


def _jwk(public_key, key_id: str) -> PyJWK:
	jwk_data = json.loads(RSAAlgorithm.to_jwk(public_key))
	jwk_data["kid"] = key_id
	return PyJWK.from_json(json.dumps(jwk_data))


def _token(private_key, key_id: str, audience: str = AUDIENCE, ttl_seconds: int = 3600) -> str:
	payload = {"sub": "controller", "aud": audience, "exp": int(time.time()) + ttl_seconds}
	return jwt.encode(payload, private_key, algorithm="RS256", headers={"kid": key_id})


def _authentication(path: Path, jwk: PyJWK) -> Authentication:
	write_config(path)
	authentication = Authentication(path)
	authentication._jwks_client = _FakeJWKClient([jwk])
	authentication._jwks_url = "https://issuer.example.com/jwks.json"
	return authentication


def test_require_accepts_a_valid_jwks_token(tmp_path):
	private_key = _key_pair()
	authentication = _authentication(tmp_path / "proxy-control.toml", _jwk(private_key.public_key(), "key-1"))

	authentication.require(authorization=f"Bearer {_token(private_key, 'key-1')}")


@pytest.mark.parametrize("authorization", [None, "token", "Basic token"])
def test_require_rejects_a_missing_bearer_token(tmp_path, authorization):
	with pytest.raises(HTTPException):
		Authentication(write_config(tmp_path / "proxy-control.toml")).require(authorization)


def test_require_rejects_the_cluster_password(tmp_path):
	path = write_config(tmp_path / "proxy-control.toml", has_jwks=False)
	with path.open("a") as target:
		target.write(
			'\n[cluster]\nnode_id = "proxy-001"\npassword = "cluster-secret"\n'
			'peers = [{ node_id = "proxy-001", address = "https://proxy-001.example.com" }]\n'
		)

	with pytest.raises(HTTPException):
		Authentication(path).require("Bearer cluster-secret")


def test_require_reads_a_changed_password_without_a_restart(tmp_path):
	path = write_config(tmp_path / "proxy-control.toml", has_jwks=False, password=PASSWORD)
	authentication = Authentication(path)
	authentication.require(f"Bearer {PASSWORD}")

	write_config(path, has_jwks=False, password="rotated-password")

	authentication.require("Bearer rotated-password")
	with pytest.raises(HTTPException):
		authentication.require(f"Bearer {PASSWORD}")


def test_require_rejects_a_missing_or_malformed_config(tmp_path):
	for path in (tmp_path / "missing.toml", tmp_path / "malformed.toml"):
		if path.name == "malformed.toml":
			path.write_text("[auth\n")
		with pytest.raises(HTTPException):
			Authentication(path).require("Bearer token")


@pytest.mark.parametrize(
	("key_id", "audience", "ttl_seconds"),
	[
		("unknown-key", AUDIENCE, 3600),
		("key-1", "another-audience", 3600),
		("key-1", AUDIENCE, -60),
	],
)
def test_require_rejects_an_invalid_jwks_token(tmp_path, key_id, audience, ttl_seconds):
	private_key = _key_pair()
	authentication = _authentication(tmp_path / "proxy-control.toml", _jwk(private_key.public_key(), "key-1"))
	token = _token(private_key, key_id, audience, ttl_seconds)

	with pytest.raises(HTTPException):
		authentication.require(f"Bearer {token}")
