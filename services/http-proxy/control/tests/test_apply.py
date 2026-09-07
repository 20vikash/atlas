import datetime
from pathlib import Path

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

from proxy_control import apply

WILDCARD = "*.par-1.example.com"


def _key_and_certificate(dns_name: str = WILDCARD) -> tuple[str, str]:
	private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
	name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, dns_name)])
	not_valid_before = datetime.datetime.now(datetime.UTC) - datetime.timedelta(minutes=1)
	certificate = (
		x509.CertificateBuilder()
		.subject_name(name)
		.issuer_name(name)
		.public_key(private_key.public_key())
		.serial_number(x509.random_serial_number())
		.not_valid_before(not_valid_before)
		.not_valid_after(not_valid_before + datetime.timedelta(days=30))
		.add_extension(x509.SubjectAlternativeName([x509.DNSName(dns_name)]), critical=False)
		.sign(private_key, hashes.SHA256())
	)
	return (
		certificate.public_bytes(serialization.Encoding.PEM).decode(),
		private_key.private_bytes(
			encoding=serialization.Encoding.PEM,
			format=serialization.PrivateFormat.PKCS8,
			encryption_algorithm=serialization.NoEncryption(),
		).decode(),
	)


def _write_config(tmp_path: Path, monkeypatch, body: str) -> Path:
	path = tmp_path / "proxy-control.toml"
	path.write_text(body)
	monkeypatch.setenv("ATLAS_PROXY_CONTROL_CONFIG", str(path))
	return path


def _tls_config(tmp_path: Path, monkeypatch, certificate: str, private_key: str) -> Path:
	cert_dir = tmp_path / "certs"
	_write_config(
		tmp_path,
		monkeypatch,
		f"""
[control]
cert_dir = "{cert_dir}"

[tls]
wildcard_domain = "{WILDCARD}"
fullchain_pem = '''
{certificate.strip()}
'''
private_key_pem = '''
{private_key.strip()}
'''
""",
	)
	return cert_dir


@pytest.fixture(autouse=True)
def stub_openresty(monkeypatch):
	"""The test host has no OpenResty, so the reload cannot run."""
	monkeypatch.setattr("proxy_control.certificates.CertificateStore._reload_openresty", lambda self: None)


def test_apply_installs_the_certificate_from_the_config_file(tmp_path: Path, monkeypatch):
	certificate, private_key = _key_and_certificate()
	cert_dir = _tls_config(tmp_path, monkeypatch, certificate, private_key)

	assert apply.main() == 0

	region = cert_dir / "par-1.example.com"
	assert (region / "fullchain.pem").read_text() == certificate.strip()
	assert (cert_dir / "fullchain.pem").resolve() == (region / "fullchain.pem").resolve()
	assert (tmp_path / "region").read_text() == "par-1.example.com\n"


def test_apply_refuses_a_key_that_does_not_match(tmp_path: Path, monkeypatch):
	certificate, _ = _key_and_certificate()
	_, other_key = _key_and_certificate()
	_tls_config(tmp_path, monkeypatch, certificate, other_key)

	assert apply.main() == 1


def test_apply_refuses_a_certificate_for_another_domain(tmp_path: Path, monkeypatch):
	certificate, private_key = _key_and_certificate("*.other.example.com")
	_tls_config(tmp_path, monkeypatch, certificate, private_key)

	assert apply.main() == 1


def test_apply_refuses_a_tls_enabled_flag(tmp_path: Path, monkeypatch):
	_write_config(tmp_path, monkeypatch, "[tls]\nenabled = false\n")

	assert apply.main() == 2


# An empty file is a configuration fault.
def test_apply_reports_a_missing_certificate(tmp_path: Path, monkeypatch):
	_write_config(tmp_path, monkeypatch, '[auth]\npassword_hash = "$2b$12$hash"\n')

	assert apply.main() == 2


def test_apply_reports_a_broken_config_file(tmp_path: Path, monkeypatch):
	_write_config(tmp_path, monkeypatch, "[tls\n")

	assert apply.main() == 2
