from __future__ import annotations

from datetime import UTC, datetime, timedelta, timezone
from ipaddress import ip_address

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID
from frappe.tests import UnitTestCase

from atlas.atlas.core.tls.certificate import (
	CertificateError,
	create_certificate_authority,
	create_certificate_request,
	create_private_key,
	issue_certificate,
	read_certificate,
	serialize_private_key,
	verify_issued_certificate,
	verify_key_pair,
)


def self_signed_certificate(private_key, dns_names: list[str], expires_in_days: int = 90) -> str:
	"""Build one self-signed PEM certificate. Issuance tests reuse this helper."""
	name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, dns_names[0])])
	not_valid_before = datetime.now(UTC) - timedelta(minutes=1)
	certificate = (
		x509.CertificateBuilder()
		.subject_name(name)
		.issuer_name(name)
		.public_key(private_key.public_key())
		.serial_number(x509.random_serial_number())
		.not_valid_before(not_valid_before)
		.not_valid_after(not_valid_before + timedelta(days=expires_in_days))
		.add_extension(
			x509.SubjectAlternativeName([x509.DNSName(value) for value in dns_names]), critical=False
		)
		.sign(private_key, hashes.SHA256())
	)
	return certificate.public_bytes(serialization.Encoding.PEM).decode()


class TestCertificate(UnitTestCase):
	def test_read_certificate_returns_expiry_and_names(self) -> None:
		private_key = create_private_key()
		certificate = self_signed_certificate(private_key, ["*.example.com", "example.com"])

		details = read_certificate(certificate)

		self.assertEqual(details.dns_names, ("*.example.com", "example.com"))
		self.assertEqual(details.expires_on.tzinfo, UTC)
		self.assertGreater(details.expires_on, datetime.now(UTC) + timedelta(days=89))

	def test_read_certificate_uses_the_leaf_of_a_chain(self) -> None:
		leaf = self_signed_certificate(create_private_key(), ["*.example.com"])
		issuer = self_signed_certificate(create_private_key(), ["ca.example.com"])

		details = read_certificate(leaf + issuer)

		self.assertEqual(details.dns_names, ("*.example.com",))

	def test_read_certificate_rejects_text_that_is_not_pem(self) -> None:
		with self.assertRaises(CertificateError):
			read_certificate("not a certificate")

	def test_verify_key_pair_accepts_the_matching_key(self) -> None:
		private_key = create_private_key()
		certificate = self_signed_certificate(private_key, ["*.example.com"])

		verify_key_pair(certificate, serialize_private_key(private_key))

	def test_verify_key_pair_rejects_another_key(self) -> None:
		certificate = self_signed_certificate(create_private_key(), ["*.example.com"])

		with self.assertRaises(CertificateError):
			verify_key_pair(certificate, serialize_private_key(create_private_key()))

	def test_certificate_request_carries_the_requested_names(self) -> None:
		private_key = create_private_key()

		request = x509.load_der_x509_csr(create_certificate_request(private_key, ["*.example.com"]))

		names = request.extensions.get_extension_for_class(x509.SubjectAlternativeName)
		self.assertEqual(names.value.get_values_for_type(x509.DNSName), ["*.example.com"])
		self.assertTrue(request.is_signature_valid)

	def test_regional_ca_issues_a_metal_client_and_server_certificate(self) -> None:
		ca_certificate, ca_private_key = create_certificate_authority("Atlas Metal CA test")
		addresses = [ip_address("fdab::12"), ip_address("10.0.0.12"), ip_address("192.0.2.12")]

		certificate, private_key = issue_certificate(
			ca_certificate,
			ca_private_key,
			"metal-12.example.test",
			addresses,
		)

		verify_key_pair(certificate, private_key)
		verify_issued_certificate(certificate, ca_certificate)
		details = read_certificate(certificate)
		self.assertEqual(details.dns_names, ("metal-12.example.test",))
		self.assertEqual(set(details.ip_addresses), set(addresses))
		leaf = x509.load_pem_x509_certificate(certificate.encode())
		usage = leaf.extensions.get_extension_for_class(x509.ExtendedKeyUsage).value
		self.assertEqual(set(usage), {ExtendedKeyUsageOID.SERVER_AUTH, ExtendedKeyUsageOID.CLIENT_AUTH})

	def test_issued_certificate_rejects_another_certificate_authority(self) -> None:
		ca_certificate, ca_private_key = create_certificate_authority("Atlas Metal CA test")
		certificate, _ = issue_certificate(
			ca_certificate,
			ca_private_key,
			"metal-12.example.test",
			[ip_address("192.0.2.12")],
		)
		other_ca_certificate, _ = create_certificate_authority("Atlas Metal CA other")

		with self.assertRaises(CertificateError):
			verify_issued_certificate(certificate, other_ca_certificate)
