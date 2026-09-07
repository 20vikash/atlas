from __future__ import annotations

import hashlib
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

		return "\n".join(
			(
				"[control]",
				f'domain = "{self.proxy_server.get_domain()}"',
				"",
				"[auth]",
				f'password_hash = "{self.password_hash}"',
				f'jwks_url = "{self.settings.proxy_jwks_url or ""}"',
				f'jwks_audience_id = "{self.settings.proxy_jwks_audience_id or ""}"',
				"",
				"[tls]",
				f'wildcard_domain = "{self.wildcard_domain}"',
				f"fullchain_pem = '''\n{certificate.strip()}\n'''",
				f"private_key_pem = '''\n{private_key.strip()}\n'''",
				"",
			)
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
		"""Return the digest of the configuration inputs."""
		password = self.proxy_server.get_password("control_api_password", raise_exception=False) or ""
		values = (
			self.proxy_server.get_domain(),
			self.wildcard_domain,
			password,
			self.settings.proxy_jwks_url or "",
			self.settings.proxy_jwks_audience_id or "",
			self.settings.get_password("wildcard_tls_certificate", raise_exception=False) or "",
			self.settings.get_password("wildcard_tls_private_key", raise_exception=False) or "",
		)
		return hashlib.sha256("\0".join(values).encode()).hexdigest()

	def get_push_command(self) -> str:
		"""Return commands to update the configuration."""
		return "\n".join(
			(
				"set -eu",
				"install -d -m 0750 /etc/atlas",
				f"install -m 0600 /dev/null {CONFIG_PATH}",
				f"cat > {CONFIG_PATH} <<'ATLAS_PROXY_CONFIG_END'",
				self.content,
				"ATLAS_PROXY_CONFIG_END",
				APPLY_COMMAND,
				f"systemctl enable --now {DAEMON_UNIT}",
				f"systemctl restart {DAEMON_UNIT}",
			)
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
