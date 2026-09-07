import os
import tomllib
from dataclasses import dataclass
from pathlib import Path

CONFIG_PATH = Path("/etc/atlas/proxy-control.toml")
DEFAULT_ADMIN_SOCKET = "/run/nginx/admin.sock"
DEFAULT_CERT_DIR = Path("/var/lib/nginx/certs")
LISTEN_ADDRESS = "127.0.0.1"


class ConfigError(Exception):
	"""The configuration is invalid."""


@dataclass(frozen=True)
class AuthConfig:
	"""Caller credentials."""

	password_hash: str = ""
	jwks_url: str = ""
	jwks_audience_id: str = ""


@dataclass(frozen=True)
class TLSConfig:
	"""Wildcard certificate configuration."""

	wildcard_domain: str
	fullchain_pem: str
	private_key_pem: str


@dataclass(frozen=True)
class ControlConfig:
	"""Control daemon configuration."""

	tls: TLSConfig
	admin_socket: str = DEFAULT_ADMIN_SOCKET
	cert_dir: Path = DEFAULT_CERT_DIR
	domain: str = ""
	auth: AuthConfig = AuthConfig()

	@property
	def reserved_subdomain(self) -> str:
		"""Return the control subdomain."""
		return self.domain.partition(".")[0]


def config_path() -> Path:
	"""Return the configuration path."""
	return Path(os.environ.get("ATLAS_PROXY_CONTROL_CONFIG") or CONFIG_PATH)


def load(path: Path | None = None) -> ControlConfig:
	"""Load the control configuration."""
	document = _read(path or config_path())
	control = _section(document, "control")
	auth = _section(document, "auth")
	if "port" in control:
		raise ConfigError("control.port is fixed at 9000")

	tls = _tls(_section(document, "tls"))
	return ControlConfig(
		admin_socket=_text(control, "admin_socket") or DEFAULT_ADMIN_SOCKET,
		cert_dir=Path(_text(control, "cert_dir") or DEFAULT_CERT_DIR),
		domain=_domain(_text(control, "domain"), tls),
		auth=AuthConfig(
			password_hash=_text(auth, "password_hash"),
			jwks_url=_text(auth, "jwks_url"),
			jwks_audience_id=_text(auth, "jwks_audience_id"),
		),
		tls=tls,
	)


def _domain(domain: str, tls: TLSConfig) -> str:
	"""Validate the control domain."""
	if not domain:
		return ""

	domain = domain.lower()
	zone = tls.wildcard_domain.lower().removeprefix("*.")
	label, separator, rest = domain.partition(".")
	if not label or not separator or rest != zone:
		raise ConfigError(f"control.domain {domain} must be one label below {zone}")

	return domain


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


def _tls(section: dict[str, object]) -> TLSConfig:
	"""Return the required wildcard certificate."""
	if "enabled" in section:
		raise ConfigError("tls.enabled is not supported")

	wildcard_domain = _text(section, "wildcard_domain")
	fullchain_pem = _text(section, "fullchain_pem")
	private_key_pem = _text(section, "private_key_pem")
	if not (wildcard_domain and fullchain_pem and private_key_pem):
		raise ConfigError("[tls] needs wildcard_domain, fullchain_pem, and private_key_pem")

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
