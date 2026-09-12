from __future__ import annotations

from dataclasses import dataclass, fields
from typing import Any

import frappe
from frappe import _
from frappe.utils import add_days, get_datetime, now_datetime

from atlas.atlas.doctype.atlas_settings.atlas_settings import WILDCARD_TLS_RENEWAL_WINDOW_DAYS
from atlas.auth.jwks import sync_central_jwks
from atlas.metal_server.core.catalog_sync import CatalogSynchronizer


@dataclass(frozen=True, slots=True)
class AtlasSetupConfiguration:
	"""Store the operator values for one Atlas region."""

	server_provider: str
	dns_provider: str
	region_name: str
	region_id: int
	wildcard_domain: str
	private_network_cidr: str
	private_network_mtu: int
	central_jwks_url: str
	public_ssh_key: str
	scaleway_organization_id: str
	scaleway_project_id: str
	scaleway_zone: str
	scaleway_machine_billing_cycle: str
	scaleway_access_key: str
	scaleway_secret_key: str
	route53_access_key_id: str
	route53_access_key_secret: str
	letsencrypt_email: str
	is_letsencrypt_staging: bool
	is_wildcard_tls_auto_renew_enabled: bool

	@classmethod
	def from_dict(cls, values: Any) -> "AtlasSetupConfiguration":
		"""Create a configuration from the deployment command input."""
		if not isinstance(values, dict):
			raise ValueError("Atlas setup configuration must be a JSON object")

		expected_fields = {field.name for field in fields(cls)}
		if set(values) != expected_fields:
			missing = sorted(expected_fields - set(values))
			unknown = sorted(set(values) - expected_fields)
			parts = []
			if missing:
				parts.append(f"missing fields: {', '.join(missing)}")
			if unknown:
				parts.append(f"unknown fields: {', '.join(unknown)}")
			raise ValueError("Invalid Atlas setup configuration; " + "; ".join(parts))

		string_fields = expected_fields - {
			"region_id",
			"private_network_mtu",
			"is_letsencrypt_staging",
			"is_wildcard_tls_auto_renew_enabled",
		}
		for field in string_fields:
			value = values[field]
			if not isinstance(value, str) or (field != "central_jwks_url" and not value.strip()):
				raise ValueError(f"Atlas setup field {field} must be a string")
		for field in ("region_id", "private_network_mtu"):
			value = values[field]
			if not isinstance(value, int) or isinstance(value, bool):
				raise ValueError(f"Atlas setup field {field} must be an integer")
		if not 0 <= values["region_id"] <= 65_535:
			raise ValueError("Atlas setup field region_id must be from 0 through 65535")
		if values["private_network_mtu"] <= 0:
			raise ValueError("Atlas setup field private_network_mtu must be positive")
		for field in ("is_letsencrypt_staging", "is_wildcard_tls_auto_renew_enabled"):
			if not isinstance(values[field], bool):
				raise ValueError(f"Atlas setup field {field} must be true or false")

		return cls(**values)

	def settings_values(self) -> dict[str, object]:
		"""Return the values that belong to Atlas Settings."""
		return {field.name: getattr(self, field.name) for field in fields(self)}


class AtlasSetup:
	"""Reconcile one Atlas region from operator configuration."""

	SERVER_IMMUTABLE_FIELDS = (
		"server_provider",
		"region_name",
		"region_id",
		"private_network_cidr",
		"public_ssh_key",
		"scaleway_organization_id",
		"scaleway_project_id",
		"scaleway_zone",
	)
	DNS_IMMUTABLE_FIELDS = ("dns_provider", "wildcard_domain")

	def __init__(self, configuration: AtlasSetupConfiguration) -> None:
		self.configuration = configuration
		self.settings = frappe.get_single("Atlas Settings")

	def run(self) -> None:
		"""Apply settings and complete each setup phase."""
		self._validate_immutable_values()
		self.settings.update(self.configuration.settings_values())
		self.settings.save(ignore_permissions=True)
		frappe.db.commit()  # nosemgrep: a setup checkpoint must survive a later provider failure

		self._setup_server_provider()
		self._setup_dns_provider()
		self._sync_catalogs()
		self._sync_central_keys()
		self._issue_wildcard_certificate()
		self._validate_result()

	def _validate_immutable_values(self) -> None:
		if self.settings.is_server_provider_setup_completed:
			self._validate_fields(self.SERVER_IMMUTABLE_FIELDS)
		if self.settings.is_dns_setup_completed:
			self._validate_fields(self.DNS_IMMUTABLE_FIELDS)

	def _validate_fields(self, fields: tuple[str, ...]) -> None:
		for field in fields:
			current = self.settings.get(field)
			configured = getattr(self.configuration, field)
			if current != configured:
				frappe.throw(
					_("Atlas setup cannot change {0} from {1!r} to {2!r} after provider setup.").format(
						field, current, configured
					)
				)

	def _setup_server_provider(self) -> None:
		if self.settings.is_server_provider_setup_completed:
			return

		self.settings.server_provider_controller.setup_infrastructure()
		frappe.db.commit()  # nosemgrep: keep provider resource IDs when a later phase fails

	def _setup_dns_provider(self) -> None:
		provider = self.settings.dns_provider_controller
		zone_id = provider.find_public_zone_id(self.settings.wildcard_domain)
		if not zone_id:
			frappe.throw(
				_("Route53 needs an existing public hosted zone for {0}.").format(
					self.settings.wildcard_domain
				)
			)
		if self.settings.route53_dns_zone_id and self.settings.route53_dns_zone_id != zone_id:
			frappe.throw(
				_("Route53 public zone {0} does not match stored zone {1}.").format(
					zone_id, self.settings.route53_dns_zone_id
				)
			)

		if self.settings.is_dns_setup_completed:
			return

		self.settings.route53_dns_zone_id = zone_id
		provider.bootstrap()
		frappe.db.commit()  # nosemgrep: keep DNS setup when a later phase fails

	def _sync_catalogs(self) -> None:
		catalog = CatalogSynchronizer(self.settings.server_provider_controller)
		catalog.sync_server_sizes()
		catalog.sync_server_images()
		frappe.db.commit()  # nosemgrep: keep catalog data when a later phase fails

	def _sync_central_keys(self) -> None:
		if not sync_central_jwks():
			frappe.throw(_("Atlas could not get the configured Central JSON Web Key Set."))
		frappe.db.commit()  # nosemgrep: keep a valid key set when certificate issuance fails

	def _issue_wildcard_certificate(self) -> None:
		if not self._certificate_needs_renewal():
			return

		self.settings.issue_wildcard_certificate()
		frappe.db.commit()  # nosemgrep: certificate issuance is an external setup checkpoint

	def _certificate_needs_renewal(self) -> bool:
		expires_on = self.settings.wildcard_tls_expires_on
		if not expires_on:
			return True

		return get_datetime(expires_on) <= add_days(now_datetime(), WILDCARD_TLS_RENEWAL_WINDOW_DAYS)

	def _validate_result(self) -> None:
		self.settings.reload()
		if not self.settings.is_setup_completed:
			frappe.throw(_("Atlas Settings setup is not complete."))
		if not self.settings.wildcard_tls_expires_on:
			frappe.throw(_("Atlas Settings has no wildcard TLS certificate."))
		if not frappe.db.exists("Metal Server Size", {"provider_type": self.settings.server_provider}):
			frappe.throw(_("Atlas has no Metal Server Size for the configured provider."))
		if not frappe.db.exists("Metal Server Image", {"provider_type": self.settings.server_provider}):
			frappe.throw(_("Atlas has no Metal Server Image for the configured provider."))
