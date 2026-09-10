from __future__ import annotations

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey


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
