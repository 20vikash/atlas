from __future__ import annotations

from datetime import UTC, datetime, timedelta
from typing import TYPE_CHECKING, Any
from uuid import uuid4

import jwt
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, NoEncryption, PrivateFormat

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings

CARGO_SUBJECT = "cargo"
TOKEN_LIFETIME = timedelta(minutes=5)


def initialize_signing_key(settings: AtlasSettings, *, persist: bool = False) -> bool:
	"""Create a regional signing key when Atlas has no key for this region."""
	prefix = f"{settings.issuer}:"
	private_key = settings.get_password("jwt_signing_private_key", raise_exception=False)
	if private_key and (settings.jwt_signing_key_id or "").startswith(prefix):
		return False

	key = Ed25519PrivateKey.generate()
	private_key = key.private_bytes(
		Encoding.PEM,
		PrivateFormat.PKCS8,
		NoEncryption(),
	).decode()
	key_id = f"{prefix}{uuid4()}"
	settings.jwt_signing_private_key = private_key
	settings.jwt_signing_key_id = key_id
	if persist:
		previous_ignore_validate = settings.flags.ignore_validate
		settings.flags.ignore_validate = True
		try:
			settings.save()
		finally:
			settings.flags.ignore_validate = previous_ignore_validate
	return True


def issue_cargo_tokens(settings: AtlasSettings) -> dict[str, str]:
	"""Return separate Atlas and Proxy credentials for Cargo."""
	now = datetime.now(UTC)
	common = {
		"iss": settings.issuer,
		"sub": CARGO_SUBJECT,
		"iat": now,
		"nbf": now,
		"exp": now + TOKEN_LIFETIME,
	}

	return {
		"atlas": _encode(
			settings,
			{
				**common,
				"aud": settings.admin_audience_id,
				"scope": "*",
				"tenant": "1",
			},
		),
		"proxy": _encode(
			settings,
			{
				**common,
				"aud": settings.proxy_audience_id,
				"scope": "site:*",
				"constraints": {"site": {"suffix": "-svc"}},
			},
		),
	}


def _encode(settings: AtlasSettings, claims: dict[str, Any]) -> str:
	private_key = settings.get_password("jwt_signing_private_key", raise_exception=False)
	if not private_key or not settings.jwt_signing_key_id:
		raise RuntimeError("Atlas has no JSON Web Token signing key.")

	return jwt.encode(
		claims,
		private_key,
		algorithm="EdDSA",
		headers={"kid": settings.jwt_signing_key_id},
	)
