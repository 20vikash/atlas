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


class ProxyConfiguration:
	"""Render the configuration file one Proxy Server needs.

	The file carries the wildcard private key and the control credential, so the
	caller writes it over SSH at mode 0600 and never through an `SSH Task`, whose
	script and environment are stored as plain text.
	"""

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
				"[auth]",
				f'password_hash = "{self.password_hash}"',
				f'jwks_url = "{self.settings.proxy_jwks_url or ""}"',
				f'jwks_audience_id = "{self.settings.proxy_jwks_audience_id or ""}"',
				"",
				"[tls]",
				"enabled = true",
				f'wildcard_domain = "{self.wildcard_domain}"',
				f"fullchain_pem = '''\n{certificate.strip()}\n'''",
				f"private_key_pem = '''\n{private_key.strip()}\n'''",
				"",
			)
		)

	@property
	def password_hash(self) -> str:
		"""Return the bcrypt hash of the control API password.

		Atlas keeps the password and the proxy keeps the hash, so a host that is
		read by an attacker gives up no credential.
		"""
		password = self.proxy_server.get_password("control_api_password", raise_exception=False)
		if not password:
			frappe.throw(_("Proxy Server {0} has no control API password.").format(self.proxy_server.name))

		return bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()

	@property
	def digest(self) -> str:
		"""Return the digest of everything the file carries except the password salt.

		A bcrypt hash is salted, so the rendered file differs on every call. The
		digest therefore covers the inputs, which is what decides a resend.
		"""
		password = self.proxy_server.get_password("control_api_password", raise_exception=False) or ""
		values = (
			self.wildcard_domain,
			password,
			self.settings.proxy_jwks_url or "",
			self.settings.proxy_jwks_audience_id or "",
			self.settings.get_password("wildcard_tls_certificate", raise_exception=False) or "",
			self.settings.get_password("wildcard_tls_private_key", raise_exception=False) or "",
		)
		return hashlib.sha256("\0".join(values).encode()).hexdigest()

	def get_push_command(self) -> str:
		"""Return the remote command that writes the file and applies it.

		The content arrives on stdin of the remote `cat`, so it never reaches the
		process list of the host.
		"""
		return "\n".join(
			(
				"set -eu",
				"install -d -m 0750 /etc/atlas",
				f"install -m 0600 /dev/null {CONFIG_PATH}",
				f"cat > {CONFIG_PATH} <<'ATLAS_PROXY_CONFIG_END'",
				self.content,
				"ATLAS_PROXY_CONFIG_END",
				f"{APPLY_COMMAND}",
			)
		)


def push_configuration_to_active_proxies() -> None:
	"""Send the current configuration to every active Proxy Server.

	The wildcard certificate changes when Atlas renews it or when an operator
	replaces it. Both paths reach the proxies through this function.
	"""
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
