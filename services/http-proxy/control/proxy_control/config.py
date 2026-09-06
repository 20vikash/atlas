import os
import tomllib
from dataclasses import dataclass
from pathlib import Path

CONFIG_PATH = Path("/etc/atlas/proxy-control.toml")
DEFAULT_PORT = 9000
DEFAULT_ADMIN_SOCKET = "/run/nginx/admin.sock"
DEFAULT_CERT_DIR = Path("/var/lib/nginx/certs")


class ConfigError(Exception):
	"""Raised when the configuration file cannot be read or has the wrong shape."""


@dataclass(frozen=True)
class AuthConfig:
	"""The credentials a caller can present. An empty value disables that method."""

	password_hash: str = ""
	jwks_url: str = ""
	jwks_audience_id: str = ""


@dataclass(frozen=True)
class TLSConfig:
	"""The regional wildcard certificate. Atlas pushes the PEM text in the file."""

	wildcard_domain: str
	fullchain_pem: str
	private_key_pem: str
	enabled: bool = True


@dataclass(frozen=True)
class ControlConfig:
	"""Everything the control daemon and the apply command read."""

	port: int = DEFAULT_PORT
	admin_socket: str = DEFAULT_ADMIN_SOCKET
	cert_dir: Path = DEFAULT_CERT_DIR
	auth: AuthConfig = AuthConfig()
	tls: TLSConfig | None = None


def config_path() -> Path:
	"""Return the configuration path. `ATLAS_PROXY_CONTROL_CONFIG` overrides the default."""
	return Path(os.environ.get("ATLAS_PROXY_CONTROL_CONFIG") or CONFIG_PATH)


def load(path: Path | None = None) -> ControlConfig:
	"""Read the configuration file.

	A missing file gives the defaults with no credentials, and TLS stays on, so
	an unconfigured proxy fails here instead of serving its placeholder
	certificate to real traffic.
	"""
	document = _read(path or config_path())
	control = _section(document, "control")
	auth = _section(document, "auth")

	return ControlConfig(
		port=_integer(control, "port", DEFAULT_PORT),
		admin_socket=_text(control, "admin_socket") or DEFAULT_ADMIN_SOCKET,
		cert_dir=Path(_text(control, "cert_dir") or DEFAULT_CERT_DIR),
		auth=AuthConfig(
			password_hash=_text(auth, "password_hash"),
			jwks_url=_text(auth, "jwks_url"),
			jwks_audience_id=_text(auth, "jwks_audience_id"),
		),
		tls=_tls(_section(document, "tls")),
	)


def _read(path: Path) -> dict[str, object]:
	try:
		with path.open("rb") as source:
			return tomllib.load(source)
	except FileNotFoundError:
		return {}
	except OSError as error:
		raise ConfigError(f"cannot read {path}: {error}") from error
	except tomllib.TOMLDecodeError as error:
		raise ConfigError(f"{path} is not valid TOML: {error}") from error


def _tls(section: dict[str, object]) -> TLSConfig | None:
	"""Return the certificate, or None when TLS is off.

	TLS is on unless the file turns it off, so a proxy without a certificate
	refuses to start instead of serving its placeholder to real traffic.
	"""
	if not _boolean(section, "enabled", True):
		return None

	wildcard_domain = _text(section, "wildcard_domain")
	fullchain_pem = _text(section, "fullchain_pem")
	private_key_pem = _text(section, "private_key_pem")
	if not (wildcard_domain and fullchain_pem and private_key_pem):
		raise ConfigError(
			"[tls] needs wildcard_domain, fullchain_pem, and private_key_pem. "
			"Set enabled = false to run without a wildcard certificate."
		)

	return TLSConfig(wildcard_domain, fullchain_pem, private_key_pem)


def _section(document: dict[str, object], name: str) -> dict[str, object]:
	value = document.get(name, {})
	if not isinstance(value, dict):
		raise ConfigError(f"[{name}] must be a table")
	return value


def _text(section: dict[str, object], key: str) -> str:
	value = section.get(key, "")
	if not isinstance(value, str):
		raise ConfigError(f"{key} must be a string")
	return value.strip()


def _integer(section: dict[str, object], key: str, default: int) -> int:
	value = section.get(key, default)
	if not isinstance(value, int) or isinstance(value, bool):
		raise ConfigError(f"{key} must be an integer")
	return value


def _boolean(section: dict[str, object], key: str, default: bool) -> bool:
	value = section.get(key, default)
	if not isinstance(value, bool):
		raise ConfigError(f"{key} must be true or false")
	return value
