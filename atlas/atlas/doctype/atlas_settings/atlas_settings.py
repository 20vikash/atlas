# Copyright (c) 2026, Frappe and contributors
# For license information, please see license.txt

from __future__ import annotations

from functools import cached_property
from typing import TYPE_CHECKING

import frappe
from frappe import _
from frappe.model.document import Document
from frappe.utils import add_days, convert_utc_to_system_timezone, get_datetime, now_datetime

from atlas.service.core.configuration import push_configuration_to_active_proxies

if TYPE_CHECKING:
	from atlas.atlas.core.dns_providers.base import DnsProvider
	from atlas.atlas.core.server_providers.base import ServerProvider
	from atlas.atlas.core.tls.letsencrypt import LetsEncrypt
	from atlas.atlas.object_storage import ObjectStorageClient

WILDCARD_TLS_RENEWAL_WINDOW_DAYS = 30
# A change to one of these reaches every active proxy through its own job.
PROXY_CONFIGURATION_FIELDS = (
	"wildcard_tls_certificate",
	"wildcard_tls_private_key",
	"proxy_jwks_url",
	"proxy_jwks_audience_id",
)


class AtlasSettings(Document):
	"""Site-wide Atlas configuration and provider credentials."""

	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		dns_provider: DF.Literal["Route53"]
		http_proxy_package_file: DF.Link | None
		http_proxy_package_hash: DF.Data | None
		is_dns_setup_completed: DF.Check
		is_letsencrypt_staging: DF.Check
		is_server_provider_setup_completed: DF.Check
		is_setup_completed: DF.Check
		is_wildcard_tls_auto_renew_enabled: DF.Check
		letsencrypt_config_directory: DF.Data | None
		letsencrypt_email: DF.Data
		metald_binary_x86_64_file: DF.Link | None
		metald_source_hash: DF.Data | None
		object_storage_access_key_id: DF.Data | None
		object_storage_bucket: DF.Data | None
		object_storage_endpoint_url: DF.Data | None
		object_storage_region: DF.Data | None
		object_storage_secret_access_key: DF.Password | None
		object_storage_signed_url_expiry: DF.Int
		private_network_cidr: DF.Data
		proxy_jwks_audience_id: DF.Data | None
		proxy_jwks_url: DF.Data | None
		private_network_mtu: DF.Int
		public_ssh_key: DF.SmallText
		region_id: DF.Int
		region_name: DF.Data
		route53_access_key_id: DF.Data | None
		route53_access_key_secret: DF.Password | None
		route53_dns_zone_id: DF.Data | None
		scaleway_access_key: DF.Data | None
		scaleway_machine_billing_cycle: DF.Literal["Hourly", "Monthly"]
		scaleway_organization_id: DF.Data | None
		scaleway_private_network_id: DF.Data | None
		scaleway_project_id: DF.Data | None
		scaleway_secret_key: DF.Password | None
		scaleway_ssh_key_id: DF.Data | None
		scaleway_vpc_id: DF.Data | None
		scaleway_zone: DF.Literal[
			"fr-par-1",
			"fr-par-2",
			"fr-par-3",
			"nl-ams-1",
			"nl-ams-2",
			"nl-ams-3",
			"pl-waw-1",
			"pl-waw-2",
			"pl-waw-3",
		]
		server_provider: DF.Literal["Scaleway"]
		wg_mesh_binary_x86_64_file: DF.Link | None
		wg_mesh_source_hash: DF.Data | None
		wildcard_domain: DF.Data
		wildcard_tls_certificate: DF.Password | None
		wildcard_tls_expires_on: DF.Datetime | None
		wildcard_tls_private_key: DF.Password | None
	# end: auto-generated types

	@property
	def resource_name_prefix(self) -> str:
		"""Return the prefix Atlas puts on provider resource names."""
		return f"atlas-{self.region_name.lower()}-"

	@cached_property
	def server_provider_controller(self) -> "ServerProvider":
		"""Return the configured server provider."""
		from atlas.atlas.core.server_providers import get_server_provider

		return get_server_provider(settings=self)

	@cached_property
	def dns_provider_controller(self) -> "DnsProvider":
		"""Return the configured DNS provider."""
		from atlas.atlas.core.dns_providers import get_dns_provider

		return get_dns_provider(settings=self)

	@cached_property
	def letsencrypt_controller(self) -> "LetsEncrypt":
		"""Return the Let's Encrypt issuer for the configured wildcard domain."""
		from atlas.atlas.core.tls.letsencrypt import LetsEncrypt

		return LetsEncrypt(settings=self)

	def get_object_storage_client(self) -> "ObjectStorageClient":
		"""Create the configured object storage client."""
		from atlas.atlas.object_storage import ObjectStorageClient

		return ObjectStorageClient(
			bucket=self.object_storage_bucket,
			access_key_id=self.object_storage_access_key_id,
			secret_access_key=self.get_password("object_storage_secret_access_key", raise_exception=False),
			endpoint_url=self.object_storage_endpoint_url or "",
			region=self.object_storage_region or "",
			signed_url_expiry=self.object_storage_signed_url_expiry or 86400,
		)

	def validate(self) -> None:
		"""Reject settings that would leave Atlas unable to reach a provider."""
		if self.is_setup_completed and not (
			self.is_server_provider_setup_completed and self.is_dns_setup_completed
		):
			frappe.throw(_("Atlas Settings cannot be marked as completed before provider setup is complete."))

		self.server_provider_controller.validate_settings()
		self.dns_provider_controller.validate_settings()

		if self.wildcard_domain.startswith("*."):
			frappe.throw(
				_(
					"Remove '*.' from the wildcard domain in Atlas Settings. It is automatically added by Atlas."
				)
			)

		self.region_name = self.region_name.strip().lower()

		self.validate_wildcard_certificate()

	def validate_wildcard_certificate(self) -> None:
		"""Keep the stored certificate, its private key, and the expiry consistent."""
		from atlas.atlas.core.tls.certificate import CertificateError, read_certificate, verify_key_pair

		certificate = self.get_password("wildcard_tls_certificate", raise_exception=False)
		private_key = self.get_password("wildcard_tls_private_key", raise_exception=False)
		if not certificate:
			self.wildcard_tls_private_key = None
			self.wildcard_tls_expires_on = None
			return

		if not private_key:
			frappe.throw(_("Set the wildcard TLS private key together with the certificate."))

		try:
			verify_key_pair(certificate, private_key)
			details = read_certificate(certificate)
		except CertificateError as error:
			frappe.throw(str(error))

		if f"*.{self.wildcard_domain}" not in details.dns_names:
			frappe.throw(
				_("The certificate covers {0} and not the wildcard domain.").format(
					", ".join(details.dns_names)
				)
			)

		self.wildcard_tls_expires_on = convert_utc_to_system_timezone(details.expires_on).replace(tzinfo=None)

	def on_update(self) -> None:
		"""Skip provider checks when the empty settings document is created."""
		if self.flags.in_insert:
			return

		if any(self.has_value_changed(field) for field in self.server_provider_controller.credential_fields):
			self.server_provider_controller.validate_credentials()

		if any(self.has_value_changed(field) for field in self.dns_provider_controller.credential_fields):
			self.dns_provider_controller.validate_credentials()

		if any(self.has_value_changed(field) for field in PROXY_CONFIGURATION_FIELDS):
			push_configuration_to_active_proxies()

	def before_save(self) -> None:
		"""Apply provider setup when the credentials change."""
		if (
			self.is_dns_setup_completed
			and self.is_server_provider_setup_completed
			and not self.is_setup_completed
		):
			self.is_setup_completed = True

	@frappe.whitelist(methods=["POST"])
	def setup_server_provider(self) -> None:
		"""Prepare the provider account for Atlas use."""
		frappe.only_for("System Manager")
		try:
			self.server_provider_controller.setup_infrastructure()
		except Exception:
			# Keep completed setup work when a later step fails.
			frappe.db.commit()  # nosemgrep
			raise

	@frappe.whitelist(methods=["POST"])
	def setup_dns_provider(self) -> None:
		"""Prepare the DNS zone for Atlas use."""
		frappe.only_for("System Manager")
		self.dns_provider_controller.bootstrap()

	@frappe.whitelist(methods=["POST"])
	def sync_server_sizes(self) -> None:
		"""Refresh the Metal Server Size catalog from the provider."""
		frappe.only_for("System Manager")
		if not self.is_setup_completed:
			frappe.throw(_("Atlas Settings must be fully set up before syncing server sizes."))

		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_sync_server_sizes",
			queue="default",
			job_id="atlas-sync-server-sizes",
			deduplicate=True,
			enqueue_after_commit=True,
		)
		frappe.msgprint(_("Metal Server sizes sync has been queued. Please check after some time."))

	def _sync_server_sizes(self) -> None:
		from atlas.metal_server.core.catalog_sync import CatalogSynchronizer

		CatalogSynchronizer(self.server_provider_controller).sync_server_sizes()

	@frappe.whitelist(methods=["POST"])
	def sync_server_images(self) -> None:
		"""Refresh the Metal Server Image catalog from the provider."""
		frappe.only_for("System Manager")
		if not self.is_setup_completed:
			frappe.throw(_("Atlas Settings must be fully set up before syncing server images."))

		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_sync_server_images",
			queue="default",
			job_id="atlas-sync-server-images",
			deduplicate=True,
			enqueue_after_commit=True,
		)
		frappe.msgprint(_("Metal Server images sync has been queued. Please check after some time."))

	def _sync_server_images(self) -> None:
		from atlas.metal_server.core.catalog_sync import CatalogSynchronizer

		CatalogSynchronizer(self.server_provider_controller).sync_server_images()

	@frappe.whitelist(methods=["POST"])
	def renew_wildcard_certificate(self) -> None:
		"""Issue a new wildcard certificate from Let's Encrypt."""
		frappe.only_for("System Manager")
		self.enqueue_wildcard_certificate_renewal()
		frappe.msgprint(_("Wildcard TLS certificate renewal has been queued. Please check after some time."))

	def enqueue_wildcard_certificate_renewal(self) -> None:
		"""Queue one issuance. A wildcard order waits for DNS, so it cannot run in a request."""
		if not self.is_dns_setup_completed:
			frappe.throw(_("Complete the DNS setup before renewing the wildcard TLS certificate."))

		frappe.enqueue_doc(
			self.doctype,
			self.name,
			"_renew_wildcard_certificate",
			queue="long",
			timeout=1800,
			job_id="atlas-renew-wildcard-certificate",
			deduplicate=True,
			enqueue_after_commit=True,
		)

	def _renew_wildcard_certificate(self) -> None:
		issued = self.letsencrypt_controller.issue_wildcard_certificate()
		self.wildcard_tls_certificate = issued.certificate_pem
		self.wildcard_tls_private_key = issued.private_key_pem
		self.save()


def renew_expiring_wildcard_certificate() -> None:
	"""Queue a renewal when auto renew is on and the certificate expires inside the window."""
	settings: AtlasSettings = frappe.get_single("Atlas Settings")
	if not settings.is_wildcard_tls_auto_renew_enabled:
		return

	renew_after = add_days(now_datetime(), WILDCARD_TLS_RENEWAL_WINDOW_DAYS)
	if settings.wildcard_tls_expires_on and get_datetime(settings.wildcard_tls_expires_on) > renew_after:
		return

	settings.enqueue_wildcard_certificate_renewal()
