from datetime import UTC, datetime, timedelta
from types import SimpleNamespace
from unittest.mock import Mock, patch

import frappe
import jwt
from cryptography.hazmat.primitives.asymmetric import rsa
from frappe.tests import UnitTestCase

from atlas.auth.request import authenticate_central_token
from atlas.auth.token import CentralTokenValidator
from atlas.auth.user import CENTRAL_ADMIN_USER

JWKS_URL = "https://issuer.example.com/jwks.json"
AUDIENCE = "atlas-1-admin"
KEY_ID = "key-1"


def build_token(private_key, audience: str = AUDIENCE, expires_in: int = 300) -> str:
	"""Return one signed token for the central issuer."""
	expires_at = datetime.now(UTC) + timedelta(seconds=expires_in)
	return jwt.encode(
		{"aud": audience, "exp": expires_at},
		private_key,
		algorithm="RS256",
		headers={"kid": KEY_ID},
	)


class TestCentralToken(UnitTestCase):
	@classmethod
	def setUpClass(cls) -> None:
		super().setUpClass()
		cls.private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)

	def validate(self, token: str, signing_key=None):
		"""Run the validator against one signed token."""
		settings = SimpleNamespace(central_jwks_url=JWKS_URL, admin_audience_id=AUDIENCE)
		key = SimpleNamespace(key=signing_key or self.private_key.public_key())
		with (
			patch("frappe.get_cached_doc", return_value=settings),
			patch.object(CentralTokenValidator, "get_client", return_value=Mock()),
			patch("jwt.PyJWKClient.match_kid", return_value=key),
		):
			return CentralTokenValidator().get_claims(token)

	def test_a_signed_token_returns_its_claims(self) -> None:
		claims = self.validate(build_token(self.private_key))

		self.assertEqual(claims["aud"], AUDIENCE)

	def test_a_token_for_another_audience_is_refused(self) -> None:
		self.assertIsNone(self.validate(build_token(self.private_key, audience="atlas-1-proxy")))

	def test_an_expired_token_is_refused(self) -> None:
		self.assertIsNone(self.validate(build_token(self.private_key, expires_in=-1)))

	def test_a_token_without_a_key_id_is_refused(self) -> None:
		token = jwt.encode({"aud": AUDIENCE, "exp": 9999999999}, self.private_key, algorithm="RS256")

		self.assertIsNone(self.validate(token))

	def test_a_token_signed_by_another_key_is_refused(self) -> None:
		other_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)

		self.assertIsNone(self.validate(build_token(other_key)))


class TestCentralTokenSession(UnitTestCase):
	def setUp(self) -> None:
		self.previous_user = frappe.session.user
		frappe.set_user("Guest")

	def tearDown(self) -> None:
		frappe.set_user(self.previous_user)

	def test_a_valid_token_becomes_the_atlas_admin_user(self) -> None:
		with (
			patch("frappe.get_request_header", return_value="a-token"),
			patch.object(CentralTokenValidator, "get_claims", return_value={"aud": AUDIENCE}),
			patch("atlas.auth.request.frappe.set_user") as set_user,
		):
			authenticate_central_token()

		set_user.assert_called_once_with(CENTRAL_ADMIN_USER)

	def test_an_invalid_token_keeps_the_guest_session(self) -> None:
		with (
			patch("frappe.get_request_header", return_value="a-token"),
			patch.object(CentralTokenValidator, "get_claims", return_value=None),
			patch("atlas.auth.request.frappe.set_user") as set_user,
		):
			authenticate_central_token()

		set_user.assert_not_called()

	def test_a_signed_in_session_is_never_replaced(self) -> None:
		frappe.set_user("Administrator")
		with (
			patch("frappe.get_request_header", return_value="a-token"),
			patch.object(CentralTokenValidator, "get_claims") as get_claims,
		):
			authenticate_central_token()

		get_claims.assert_not_called()
