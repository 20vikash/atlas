from __future__ import annotations

from atlas.api.models import JSONWebKeySetResponse
from atlas.api.router import atlas_router
from atlas.auth.jwks import merged_jwks


@atlas_router.get("jwks.json", public=True)
def get_jwks() -> JSONWebKeySetResponse:
	"""Return the public keys that this Atlas region trusts."""
	return JSONWebKeySetResponse.model_validate(merged_jwks())
