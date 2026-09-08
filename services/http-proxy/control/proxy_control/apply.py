import sys
from pathlib import Path

from .certificates import CertificateStore
from .config import ConfigError, ControlConfig, load


def main() -> int:
	"""Apply the certificate and control subdomain."""
	try:
		config = load()
	except ConfigError as error:
		print(f"atlas-proxy-control: {error}", file=sys.stderr)
		return 2

	try:
		write_openresty_configuration(config)
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


def write_openresty_configuration(config: ControlConfig) -> None:
	"""Write the values that OpenResty reads at startup."""
	state_directory = Path(config.cert_dir).parent

	control_subdomain_path = state_directory / "control-subdomain"
	control_subdomain_path.write_text(f"{','.join(config.reserved_subdomains)}\n")
	control_subdomain_path.chmod(0o644)

	auto_proxy_path = state_directory / "auto-proxy"
	host_prefixes = ",".join(config.auto_proxy_host_prefixes)
	auto_proxy_path.write_text(f"{config.auto_proxy_address_prefix}\n{host_prefixes}\n")
	auto_proxy_path.chmod(0o644)


if __name__ == "__main__":
	raise SystemExit(main())
