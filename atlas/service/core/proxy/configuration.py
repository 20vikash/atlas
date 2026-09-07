from __future__ import annotations

import hashlib
from string import Template
from typing import TYPE_CHECKING

import bcrypt
import frappe
from frappe import _

if TYPE_CHECKING:
	from atlas.atlas.doctype.atlas_settings.atlas_settings import AtlasSettings
	from atlas.service.doctype.proxy_server.proxy_server import ProxyServer

CONFIG_PATH = "/etc/atlas/proxy-control.toml"
APPLY_COMMAND = "/opt/atlas/proxy-control/bin/proxy-control"
DAEMON_UNIT = "atlas-proxy-control.service"
DAEMON_SOCKET_UNIT = "atlas-proxy-control.socket"

CONFIG_TEMPLATE = Template(
	"""[control]
domain = "$domain"

[auth]
password_hash = "$password_hash"
jwks_url = "$jwks_url"
jwks_audience_id = "$jwks_audience_id"

[tls]
wildcard_domain = "$wildcard_domain"
fullchain_pem = '''
$certificate
'''
private_key_pem = '''
$private_key
'''
"""
)

WRITE_COMMAND_TEMPLATE = Template(
	"""set -eu
install -d -m 0750 /etc/atlas
install -m 0600 /dev/null $config_path
cat > $config_path <<'ATLAS_PROXY_CONFIG_END'
$content
ATLAS_PROXY_CONFIG_END"""
)

APPLY_COMMAND_TEMPLATE = Template(
	"""set -eu
$apply_command
systemctl enable --now $socket_unit
systemctl enable $daemon_unit
systemctl restart $daemon_unit"""
)


class ProxyConfiguration:
	"""Render a Proxy Server configuration file."""

	def __init__(self, proxy_server: "ProxyServer") -> None:
		self.proxy_server = proxy_server
		self.settings: AtlasSettings = frappe.get_single("Atlas Settings")

	@property
	def wildcard_domain(self) -> str:
		"""Return the name the certificate covers, which also sets the proxy region."""
		return f"*.{self.settings.wildcard_domain}"

	@property
	def content(self) -> str:
		"""Return the complete configuration file."""
		certificate = self.settings.get_password("wildcard_tls_certificate", raise_exception=False)
		private_key = self.settings.get_password("wildcard_tls_private_key", raise_exception=False)
		if not certificate or not private_key:
			frappe.throw(_("Atlas Settings holds no wildcard TLS certificate to send to a proxy."))

		return CONFIG_TEMPLATE.substitute(
			domain=self.proxy_server.get_domain(),
			password_hash=self.password_hash,
			jwks_url=self.settings.proxy_jwks_url or "",
			jwks_audience_id=self.settings.proxy_jwks_audience_id or "",
			wildcard_domain=self.wildcard_domain,
			certificate=certificate.strip(),
			private_key=private_key.strip(),
		)

	@property
	def password_hash(self) -> str:
		"""Return the bcrypt hash of the control password."""
		password = self.proxy_server.get_password("control_api_password", raise_exception=False)
		if not password:
			frappe.throw(_("Proxy Server {0} has no control API password.").format(self.proxy_server.name))

		return bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()

	@property
	def digest(self) -> str:
		"""Return the digest of the configuration and its template."""
		password = self.proxy_server.get_password("control_api_password", raise_exception=False) or ""
		values = (
			CONFIG_TEMPLATE.template,
			self.proxy_server.get_domain(),
			self.wildcard_domain,
			password,
			self.settings.proxy_jwks_url or "",
			self.settings.proxy_jwks_audience_id or "",
			self.settings.get_password("wildcard_tls_certificate", raise_exception=False) or "",
			self.settings.get_password("wildcard_tls_private_key", raise_exception=False) or "",
		)
		return hashlib.sha256("\0".join(values).encode()).hexdigest()

	def get_write_command(self) -> str:
		"""Return the command that writes the configuration file."""
		return WRITE_COMMAND_TEMPLATE.substitute(config_path=CONFIG_PATH, content=self.content)

	def get_apply_command(self) -> str:
		"""Return the command that applies the configuration."""
		return APPLY_COMMAND_TEMPLATE.substitute(
			apply_command=APPLY_COMMAND, socket_unit=DAEMON_SOCKET_UNIT, daemon_unit=DAEMON_UNIT
		)


def push_configuration_to_active_proxies() -> None:
	"""Send the current configuration to each active Proxy Server."""
	for name in frappe.get_all("Proxy Server", filters={"status": "Active"}, pluck="name"):
		frappe.enqueue_doc(
			"Proxy Server",
			name,
			"_push_configuration",
			queue="long",
			timeout=600,
			job_id=f"atlas||proxy-server||configure||{name}",
			deduplicate=True,
			enqueue_after_commit=True,
		)
