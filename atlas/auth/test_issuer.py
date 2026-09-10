from types import SimpleNamespace
from unittest.mock import MagicMock

import jwt
from frappe.tests import UnitTestCase

from atlas.auth.issuer import initialize_signing_key, issue_cargo_tokens


class TestIssuer(UnitTestCase):
	def test_a_signing_key_uses_the_region_namespace(self) -> None:
		settings = SimpleNamespace(
			issuer="atlas:42",
			jwt_signing_key_id=None,
			jwt_signing_private_key=None,
			get_password=lambda *args, **kwargs: None,
		)

		self.assertTrue(initialize_signing_key(settings))
		self.assertTrue(settings.jwt_signing_key_id.startswith("atlas:42:"))
		self.assertIn("BEGIN PRIVATE KEY", settings.jwt_signing_private_key)

	def test_a_persisted_key_saves_without_validation(self) -> None:
		settings = MagicMock(issuer="atlas:42", jwt_signing_key_id=None)
		settings.get_password.return_value = None
		settings.flags.ignore_validate = False
		ignore_validate_during_save = []
		settings.save.side_effect = lambda: ignore_validate_during_save.append(settings.flags.ignore_validate)

		self.assertTrue(initialize_signing_key(settings, persist=True))

		settings.save.assert_called_once_with()
		self.assertEqual(ignore_validate_during_save, [True])
		self.assertFalse(settings.flags.ignore_validate)

	def test_cargo_receives_separate_minimal_credentials(self) -> None:
		settings = SimpleNamespace(
			issuer="atlas:42",
			admin_audience_id="atlas-admin:42",
			proxy_audience_id="atlas-proxy:42",
			jwt_signing_key_id=None,
			jwt_signing_private_key=None,
		)
		settings.get_password = lambda *args, **kwargs: settings.jwt_signing_private_key
		initialize_signing_key(settings)

		tokens = issue_cargo_tokens(settings)
		atlas_claims = jwt.decode(tokens["atlas"], options={"verify_signature": False})
		proxy_claims = jwt.decode(tokens["proxy"], options={"verify_signature": False})

		self.assertEqual(atlas_claims["aud"], "atlas-admin:42")
		self.assertEqual(atlas_claims["tenant"], "1")
		self.assertEqual(atlas_claims["scope"], "*")
		self.assertEqual(proxy_claims["aud"], "atlas-proxy:42")
		self.assertEqual(proxy_claims["scope"], "site:*")
		self.assertEqual(proxy_claims["constraints"], {"site": {"suffix": "-svc"}})
