from pathlib import Path

import pytest

from proxy_control.config import (
	DEFAULT_ADMIN_SOCKET,
	DEFAULT_CERT_DIR,
	DEFAULT_PORT,
	ConfigError,
	load,
)

FULL = """
[control]
port = 9100
admin_socket = "/run/nginx/other.sock"
cert_dir = "/srv/certs"

[auth]
password_hash = "$2b$12$hash"
jwks_url = "https://issuer.example.com/jwks.json"
jwks_audience_id = "atlas-proxy-control"

[tls]
wildcard_domain = "*.par-1.example.com"
fullchain_pem = '''
-----BEGIN CERTIFICATE-----
leaf
-----END CERTIFICATE-----
'''
private_key_pem = '''
-----BEGIN PRIVATE KEY-----
key
-----END PRIVATE KEY-----
'''
"""


# TLS is on by default, so an unconfigured proxy must not start.
def test_a_missing_file_is_refused(tmp_path: Path):
	with pytest.raises(ConfigError):
		load(tmp_path / "missing.toml")


def test_turning_tls_off_gives_the_defaults(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text("[tls]\nenabled = false\n")

	config = load(path)

	assert config.port == DEFAULT_PORT
	assert config.admin_socket == DEFAULT_ADMIN_SOCKET
	assert config.cert_dir == DEFAULT_CERT_DIR
	assert config.auth.password_hash == ""
	assert config.tls is None


def test_a_full_file_is_read(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text(FULL)

	config = load(path)

	assert config.port == 9100
	assert config.admin_socket == "/run/nginx/other.sock"
	assert config.cert_dir == Path("/srv/certs")
	assert config.auth.password_hash == "$2b$12$hash"
	assert config.auth.jwks_audience_id == "atlas-proxy-control"
	assert config.tls is not None
	assert config.tls.wildcard_domain == "*.par-1.example.com"
	assert config.tls.fullchain_pem.startswith("-----BEGIN CERTIFICATE-----")
	assert config.tls.private_key_pem.endswith("-----END PRIVATE KEY-----")


def test_a_partial_file_keeps_the_defaults(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text('[tls]\nenabled = false\n\n[auth]\npassword_hash = "$2b$12$hash"\n')

	config = load(path)

	assert config.port == DEFAULT_PORT
	assert config.auth.password_hash == "$2b$12$hash"
	assert config.auth.jwks_url == ""
	assert config.tls is None


def test_malformed_toml_is_refused(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text("[control\n")

	with pytest.raises(ConfigError):
		load(path)


def test_a_wrong_value_type_is_refused(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text('[control]\nport = "9000"\n\n[tls]\nenabled = false\n')

	with pytest.raises(ConfigError):
		load(path)


# Half a certificate would install nothing and hide the mistake.
def test_an_incomplete_tls_section_is_refused(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text('[tls]\nwildcard_domain = "*.par-1.example.com"\n')

	with pytest.raises(ConfigError):
		load(path)


def test_an_incomplete_tls_section_is_accepted_when_tls_is_off(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text('[tls]\nenabled = false\nwildcard_domain = "*.par-1.example.com"\n')

	assert load(path).tls is None


def test_a_non_boolean_enabled_is_refused(tmp_path: Path):
	path = tmp_path / "proxy-control.toml"
	path.write_text('[tls]\nenabled = "no"\n')

	with pytest.raises(ConfigError):
		load(path)


def test_the_config_path_environment_variable_wins(tmp_path: Path, monkeypatch):
	path = tmp_path / "elsewhere.toml"
	path.write_text("[control]\nport = 9200\n\n[tls]\nenabled = false\n")
	monkeypatch.setenv("ATLAS_PROXY_CONTROL_CONFIG", str(path))

	assert load().port == 9200
