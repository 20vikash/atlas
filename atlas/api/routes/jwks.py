from __future__ import annotations

from atlas.api.models import JSONWebKeySetResponse
from atlas.api.router import atlas_router
from atlas.auth.jwks import trusted_keys


@atlas_router.get("jwks.json", public=True)
def get_jwks() -> JSONWebKeySetResponse:
	"""JSON Web Key Set (JWKS).

	Returns the public keys that verify Atlas service tokens issued by this region.
	"""
	return JSONWebKeySetResponse.model_validate(trusted_keys().document)
