from types import SimpleNamespace
from unittest.mock import Mock, patch

from frappe.tests import UnitTestCase

from atlas.atlas.core.tls.certificate import create_certificate_authority
from atlas.atlas.core.tls.metal import ensure_server_certificate


class TestMetalCertificate(UnitTestCase):
	def test_server_certificate_is_saved_as_a_password(self) -> None:
		ca_certificate, ca_private_key = create_certificate_authority("Atlas Metal CA test")
		credentials = {
			"metal_tls_ca_certificate": ca_certificate,
			"metal_tls_ca_private_key": ca_private_key,
		}
		settings = SimpleNamespace(
			wildcard_domain="example.test",
			get_password=Mock(side_effect=lambda field, **_kwargs: credentials[field]),
		)
		server = SimpleNamespace(
			name="metal-12",
			settings=settings,
			wireguard_ip_address="fdab::12",
			private_ipv4_address="10.0.0.12",
			public_ipv4_address="192.0.2.12",
			get_password=Mock(return_value=None),
			save=Mock(),
		)

		returned_ca, certificate, private_key = ensure_server_certificate(server)

		self.assertEqual(returned_ca, ca_certificate)
		self.assertEqual(server.metald_tls_certificate, certificate)
		self.assertEqual(server.metald_tls_private_key, private_key)
		server.save.assert_called_once_with(ignore_permissions=True, ignore_version=True)

	def test_server_certificate_is_reissued_inside_the_renewal_window(self) -> None:
		"""A certificate that expires inside the window is replaced before it stops working."""
		ca_certificate, ca_private_key = create_certificate_authority("Atlas Metal CA test")
		credentials = {
			"metal_tls_ca_certificate": ca_certificate,
			"metal_tls_ca_private_key": ca_private_key,
		}
		settings = SimpleNamespace(
			wildcard_domain="example.test",
			get_password=Mock(side_effect=lambda field, **_kwargs: credentials[field]),
		)
		server = SimpleNamespace(
			name="metal-12",
			settings=settings,
			wireguard_ip_address="fdab::12",
			private_ipv4_address="10.0.0.12",
			public_ipv4_address="192.0.2.12",
			get_password=Mock(return_value=None),
			save=Mock(),
		)
		_, current_certificate, current_private_key = ensure_server_certificate(server)
		server.get_password = Mock(
			side_effect=lambda field, **_kwargs: {
				"metald_tls_certificate": current_certificate,
				"metald_tls_private_key": current_private_key,
			}[field]
		)

		with patch("atlas.atlas.core.tls.metal.CERTIFICATE_RENEWAL_WINDOW_DAYS", 100_000):
			_, renewed_certificate, _ = ensure_server_certificate(server)

		self.assertNotEqual(renewed_certificate, current_certificate)
		self.assertEqual(server.metald_tls_certificate, renewed_certificate)
		self.assertIsNotNone(server.metald_tls_expires_on)

	def test_current_server_certificate_is_kept(self) -> None:
		ca_certificate, ca_private_key = create_certificate_authority("Atlas Metal CA test")
		credentials = {
			"metal_tls_ca_certificate": ca_certificate,
			"metal_tls_ca_private_key": ca_private_key,
		}
		settings = SimpleNamespace(
			wildcard_domain="example.test",
			get_password=Mock(side_effect=lambda field, **_kwargs: credentials[field]),
		)
		server = SimpleNamespace(
			name="metal-12",
			settings=settings,
			wireguard_ip_address="fdab::12",
			private_ipv4_address="10.0.0.12",
			public_ipv4_address="192.0.2.12",
			get_password=Mock(return_value=None),
			save=Mock(),
		)
		_, current_certificate, current_private_key = ensure_server_certificate(server)
		server.get_password = Mock(
			side_effect=lambda field, **_kwargs: {
				"metald_tls_certificate": current_certificate,
				"metald_tls_private_key": current_private_key,
			}[field]
		)
		server.save.reset_mock()

		_, kept_certificate, _ = ensure_server_certificate(server)

		self.assertEqual(kept_certificate, current_certificate)
		server.save.assert_not_called()
