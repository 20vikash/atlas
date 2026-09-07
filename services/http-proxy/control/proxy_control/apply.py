import sys
from pathlib import Path

from .certificates import CertificateStore
from .config import ConfigError, ControlConfig, load

CONTROL_SUBDOMAIN_FILE = "control-subdomain"


def main() -> int:
	"""Apply the certificate and control subdomain."""
	try:
		config = load()
	except ConfigError as error:
		print(f"atlas-proxy-control: {error}", file=sys.stderr)
		return 2

	try:
		write_control_subdomain(config)
	except OSError as error:
		print(f"atlas-proxy-control: {error}", file=sys.stderr)
		return 1

	try:
		region = CertificateStore(config.cert_dir).install(
			config.tls.wildcard_domain,
			config.tls.fullchain_pem,
			config.tls.private_key_pem,
		)
	except (ValueError, RuntimeError) as error:
		print(f"atlas-proxy-control: {error}", file=sys.stderr)
		return 1

	print(f"atlas-proxy-control: installed the certificate for {region}")
	if config.domain:
		print(f"atlas-proxy-control: {config.domain} reaches this daemon")
	return 0


def write_control_subdomain(config: ControlConfig) -> None:
	"""Write the control labels for OpenResty."""
	path = Path(config.cert_dir).parent / CONTROL_SUBDOMAIN_FILE
	path.write_text(f"{','.join(config.reserved_subdomains)}\n")
	path.chmod(0o644)


if __name__ == "__main__":
	raise SystemExit(main())
