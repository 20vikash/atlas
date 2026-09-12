from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import frappe
from frappe.tests import UnitTestCase

from atlas.atlas.core.setup import AtlasSetup, AtlasSetupConfiguration


def configuration(**changes: object) -> AtlasSetupConfiguration:
	values = {
		"server_provider": "Scaleway",
		"dns_provider": "Route53",
		"region_name": "par-1",
		"region_id": 1,
		"wildcard_domain": "par-1.example.com",
		"private_network_cidr": "10.1.0.0/20",
		"private_network_mtu": 1500,
		"central_jwks_url": "",
		"public_ssh_key": "ssh-ed25519 AAAA atlas",
		"scaleway_organization_id": "organization",
		"scaleway_project_id": "project",
		"scaleway_zone": "fr-par-1",
		"scaleway_machine_billing_cycle": "Hourly",
		"scaleway_access_key": "access",
		"scaleway_secret_key": "secret",
		"route53_access_key_id": "route-access",
		"route53_access_key_secret": "route-secret",
		"letsencrypt_email": "ops@example.com",
		"is_letsencrypt_staging": False,
		"is_wildcard_tls_auto_renew_enabled": True,
	}
	values.update(changes)
	return AtlasSetupConfiguration.from_dict(values)


class TestAtlasSetupConfiguration(UnitTestCase):
	def test_input_needs_every_field(self) -> None:
		values = configuration().settings_values()
		values.pop("region_id")

		with self.assertRaisesRegex(ValueError, "missing fields: region_id"):
			AtlasSetupConfiguration.from_dict(values)

	def test_input_rejects_a_wrong_scalar_type(self) -> None:
		values = configuration().settings_values()
		values["private_network_mtu"] = "1500"

		with self.assertRaisesRegex(ValueError, "private_network_mtu must be an integer"):
			AtlasSetupConfiguration.from_dict(values)


class TestAtlasSetup(UnitTestCase):
	def setup(self, settings: object, **changes: object) -> AtlasSetup:
		with patch("atlas.atlas.core.setup.frappe.get_single", return_value=settings):
			return AtlasSetup(configuration(**changes))

	def test_completed_provider_refuses_immutable_drift(self) -> None:
		settings = MagicMock(is_server_provider_setup_completed=1, is_dns_setup_completed=0)
		settings.get.side_effect = lambda field: (
			"old-region" if field == "region_name" else getattr(configuration(), field)
		)
		setup = self.setup(settings)

		with self.assertRaises(frappe.ValidationError):
			setup._validate_immutable_values()

	def test_missing_public_zone_stops_before_dns_bootstrap(self) -> None:
		provider = MagicMock()
		provider.find_public_zone_id.return_value = None
		settings = SimpleNamespace(
			dns_provider_controller=provider,
			wildcard_domain="par-1.example.com",
			route53_dns_zone_id=None,
			is_dns_setup_completed=0,
		)
		setup = self.setup(settings)

		with self.assertRaises(frappe.ValidationError):
			setup._setup_dns_provider()

		provider.bootstrap.assert_not_called()

	def test_existing_public_zone_completes_dns_setup(self) -> None:
		provider = MagicMock()
		provider.find_public_zone_id.return_value = "zone-1"
		settings = SimpleNamespace(
			dns_provider_controller=provider,
			wildcard_domain="par-1.example.com",
			route53_dns_zone_id=None,
			is_dns_setup_completed=0,
		)
		setup = self.setup(settings)

		with patch("atlas.atlas.core.setup.frappe.db.commit") as commit:
			setup._setup_dns_provider()

		self.assertEqual(settings.route53_dns_zone_id, "zone-1")
		provider.bootstrap.assert_called_once_with()
		commit.assert_called_once_with()

	def test_current_certificate_is_not_replaced(self) -> None:
		settings = MagicMock(wildcard_tls_expires_on=frappe.utils.add_days(frappe.utils.now_datetime(), 60))
		setup = self.setup(settings)

		setup._issue_wildcard_certificate()

		settings.issue_wildcard_certificate.assert_not_called()
