import sys

from .certificates import CertificateStore
from .config import ConfigError, load


def main() -> int:
	"""Apply the parts of the configuration file that need a running OpenResty.

	The daemon reads its own credentials on each request, so only the certificate
	needs this command. Atlas writes the file and then runs it over SSH.
	"""
	try:
		config = load()
	except ConfigError as error:
		print(f"atlas-proxy-control: {error}", file=sys.stderr)
		return 2

	if config.tls is None:
		print("atlas-proxy-control: TLS is off, keeping the placeholder certificate")
		return 0

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
	return 0


if __name__ == "__main__":
	raise SystemExit(main())
