from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

CERTIFICATE_KEY_SIZE = 2048


class CertificateError(Exception):
	"""A certificate or private key is invalid."""


@dataclass(frozen=True)
class CertificateDetails:
	"""Issued certificate details."""

	expires_on: datetime
	dns_names: tuple[str, ...]


def read_certificate(certificate_pem: str) -> CertificateDetails:
	"""Return the leaf certificate expiry and DNS names."""
	leaf = _load_leaf(certificate_pem)
	try:
		alternative_names = leaf.extensions.get_extension_for_class(x509.SubjectAlternativeName)
	except x509.ExtensionNotFound as error:
		raise CertificateError("The certificate carries no subject alternative name.") from error

	return CertificateDetails(
		leaf.not_valid_after_utc, tuple(alternative_names.value.get_values_for_type(x509.DNSName))
	)


def verify_key_pair(certificate_pem: str, private_key_pem: str) -> None:
	"""Raise an error when the key does not match the leaf certificate."""
	leaf = _load_leaf(certificate_pem)
	try:
		private_key = serialization.load_pem_private_key(private_key_pem.encode(), password=None)
	except (ValueError, TypeError) as error:
		raise CertificateError("The private key is not valid PEM.") from error

	if _public_bytes(private_key.public_key()) != _public_bytes(leaf.public_key()):
		raise CertificateError("The private key does not match the certificate.")


def create_private_key() -> rsa.RSAPrivateKey:
	"""Create a certificate private key."""
	return rsa.generate_private_key(public_exponent=65537, key_size=CERTIFICATE_KEY_SIZE)


def serialize_private_key(private_key: rsa.RSAPrivateKey) -> str:
	"""Return an unencrypted PKCS#8 PEM private key."""
	return private_key.private_bytes(
		encoding=serialization.Encoding.PEM,
		format=serialization.PrivateFormat.PKCS8,
		encryption_algorithm=serialization.NoEncryption(),
	).decode()


def create_certificate_request(private_key: rsa.RSAPrivateKey, dns_names: list[str]) -> bytes:
	"""Return a DER certificate request for the DNS names."""
	subject = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, dns_names[0])])
	request = (
		x509.CertificateSigningRequestBuilder()
		.subject_name(subject)
		.add_extension(
			x509.SubjectAlternativeName([x509.DNSName(name) for name in dns_names]), critical=False
		)
		.sign(private_key, hashes.SHA256())
	)
	return request.public_bytes(serialization.Encoding.DER)


def _public_bytes(public_key) -> bytes:
	return public_key.public_bytes(
		encoding=serialization.Encoding.DER,
		format=serialization.PublicFormat.SubjectPublicKeyInfo,
	)


def _load_leaf(certificate_pem: str) -> x509.Certificate:
	try:
		certificates = x509.load_pem_x509_certificates(certificate_pem.encode())
	except ValueError as error:
		raise CertificateError("The certificate is not valid PEM.") from error

	if not certificates:
		raise CertificateError("The certificate chain is empty.")
	return certificates[0]
