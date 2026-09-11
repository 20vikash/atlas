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
from frappe.utils import get_datetime, now_datetime

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings

SCOPE_READ_VIRTUAL_MACHINE = "read_vm"
SCOPE_MIGRATION = "migration"
VALID_SCOPES = frozenset({SCOPE_READ_VIRTUAL_MACHINE, SCOPE_MIGRATION})
MAXIMUM_TOKEN_LIFETIME = timedelta(hours=2)
# Tokens issued before rotation remain valid for their full lifetime.
PREVIOUS_KEY_OVERLAP = MAXIMUM_TOKEN_LIFETIME


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

	@cached_property
	def previous_private_key(self) -> str | None:
		"""Return the replaced key while a token it signed can still be usable."""
		rotated_on = self.settings.metal_token_key_rotated_on
		if not rotated_on or now_datetime() > get_datetime(rotated_on) + PREVIOUS_KEY_OVERLAP:
			return None

		return self.settings.get_password("previous_metal_token_private_key", raise_exception=False)

	@property
	def current_key_id(self) -> str:
		"""Return the identifier of the key that signs a new token."""
		return get_key_id(self.current_private_key)

	def get_public_keys(self) -> list[dict[str, str]]:
		"""Return the public keys that a host must trust now."""
		private_keys = [self.current_private_key]
		if self.previous_private_key:
			private_keys.append(self.previous_private_key)

		return [
			{"id": get_key_id(private_key), "key": get_public_key(private_key)}
			for private_key in private_keys
		]

	def get_key_sync_payload(self, receiver: str) -> dict[str, object]:
		"""Return the `jwt` object that one host state exchange carries."""
		return {
			"issuer": self.settings.metal_issuer_id,
			"receiver": receiver,
			"public_keys": self.get_public_keys(),
		}

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
