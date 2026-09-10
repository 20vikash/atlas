from __future__ import annotations

import base64
import hashlib
from collections.abc import Sequence
from datetime import UTC, datetime, timedelta
from functools import cached_property
from typing import TYPE_CHECKING

import frappe
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings

SCOPE_READ_VIRTUAL_MACHINE = "read_vm"
SCOPE_MIGRATION = "migration"
VALID_SCOPES = frozenset({SCOPE_READ_VIRTUAL_MACHINE, SCOPE_MIGRATION})
MAXIMUM_TOKEN_LIFETIME = timedelta(hours=2)


class MetalTokenError(Exception):
	"""Report one refused Metal token request."""


def generate_private_key() -> str:
	"""Return one new Ed25519 signing key as PKCS8 PEM."""
	return (
		Ed25519PrivateKey.generate()
		.private_bytes(
			encoding=serialization.Encoding.PEM,
			format=serialization.PrivateFormat.PKCS8,
			encryption_algorithm=serialization.NoEncryption(),
		)
		.decode()
	)


def load_private_key(private_key_pem: str) -> Ed25519PrivateKey:
	"""Return the Ed25519 key that the PEM holds."""
	private_key = serialization.load_pem_private_key(private_key_pem.encode(), password=None)
	if not isinstance(private_key, Ed25519PrivateKey):
		raise MetalTokenError("The Metal token signing key is not an Ed25519 key")
	return private_key


def get_public_key(private_key_pem: str) -> str:
	"""Return the raw Ed25519 public key in base64url without padding."""
	return base64.urlsafe_b64encode(_raw_public_key(private_key_pem)).decode().rstrip("=")


def get_key_id(private_key_pem: str) -> str:
	"""Return the identifier that a token header carries for this key."""
	return hashlib.sha256(_raw_public_key(private_key_pem)).hexdigest()


def _raw_public_key(private_key_pem: str) -> bytes:
	return (
		load_private_key(private_key_pem)
		.public_key()
		.public_bytes(encoding=serialization.Encoding.Raw, format=serialization.PublicFormat.Raw)
	)


class MetalTokenIssuer:
	"""Issue the Atlas-signed tokens that let one Metal host call another."""

	def __init__(self, settings: AtlasSettings | None = None) -> None:
		self.settings = settings or frappe.get_cached_doc("Atlas Settings")

	@cached_property
	def current_private_key(self) -> str:
		"""Return the key that signs a new token."""
		private_key = self.settings.get_password("metal_token_private_key", raise_exception=False)
		if not private_key:
			raise MetalTokenError("Atlas Settings has no Metal token signing key")
		return private_key

	@property
	def current_key_id(self) -> str:
		"""Return the identifier of the key that signs a new token."""
		return get_key_id(self.current_private_key)

	def issue_token(
		self,
		*,
		virtual_machine_id: str,
		caller: str,
		receiver: str,
		scopes: Sequence[str],
		expires_in: timedelta = MAXIMUM_TOKEN_LIFETIME,
	) -> str:
		"""Return one token that lets the caller act on the VM at the receiver."""
		import jwt

		self._validate_request(virtual_machine_id, caller, receiver, scopes, expires_in)

		claims = {
			"vm_id": virtual_machine_id,
			"scope": list(scopes),
			"iss": self.settings.metal_issuer_id,
			"sub": caller,
			"aud": receiver,
			"exp": datetime.now(UTC) + expires_in,
		}

		return jwt.encode(
			claims,
			self.current_private_key,
			algorithm="EdDSA",
			headers={"kid": self.current_key_id},
		)

	def _validate_request(
		self,
		virtual_machine_id: str,
		caller: str,
		receiver: str,
		scopes: Sequence[str],
		expires_in: timedelta,
	) -> None:
		if not (virtual_machine_id and caller and receiver):
			raise MetalTokenError("A Metal token needs a virtual machine, a caller, and a receiver")

		if not scopes:
			raise MetalTokenError("A Metal token needs at least one scope")

		unknown_scopes = set(scopes) - VALID_SCOPES
		if unknown_scopes:
			raise MetalTokenError(f"Unknown Metal token scopes: {', '.join(sorted(unknown_scopes))}")

		if expires_in <= timedelta(0) or expires_in > MAXIMUM_TOKEN_LIFETIME:
			raise MetalTokenError("A Metal token must expire inside two hours")
