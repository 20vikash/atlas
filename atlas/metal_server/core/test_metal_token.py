# Copyright (c) 2026, Frappe and Contributors
# See license.txt

import base64
from datetime import UTC, datetime, timedelta
from unittest.mock import MagicMock

import jwt
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import load_pem_private_key
from frappe.tests import UnitTestCase
from frappe.utils import add_to_date, now_datetime

from atlas.metal_server.core.metal_token import (
	SCOPE_MIGRATION,
	SCOPE_READ_VIRTUAL_MACHINE,
	MetalTokenError,
	MetalTokenIssuer,
	generate_private_key,
	get_key_id,
	get_public_key,
)

TEST_ISSUER = "atlas-1"
TEST_CALLER = "node-fra-00002"
TEST_RECEIVER = "node-fra-00001"


class TestGeneratePrivateKey(UnitTestCase):
	def test_the_key_is_a_usable_ed25519_key(self) -> None:
		private_key = load_pem_private_key(generate_private_key().encode(), password=None)

		self.assertIsInstance(private_key, Ed25519PrivateKey)

	def test_every_key_is_new(self) -> None:
		self.assertNotEqual(generate_private_key(), generate_private_key())


class TestPublicKey(UnitTestCase):
	def setUp(self) -> None:
		self.private_key = generate_private_key()

	def test_the_public_key_is_base64url_without_padding(self) -> None:
		public_key = get_public_key(self.private_key)

		self.assertNotIn("=", public_key)
		self.assertEqual(len(base64.urlsafe_b64decode(public_key + "====")), 32)

	def test_the_key_id_is_the_sha256_of_the_public_key(self) -> None:
		import hashlib

		raw_public_key = base64.urlsafe_b64decode(get_public_key(self.private_key) + "====")

		self.assertEqual(get_key_id(self.private_key), hashlib.sha256(raw_public_key).hexdigest())

	def test_every_key_has_its_own_identifier(self) -> None:
		self.assertNotEqual(get_key_id(self.private_key), get_key_id(generate_private_key()))


class TestMetalTokenIssuer(UnitTestCase):
	def setUp(self) -> None:
		self.private_key = generate_private_key()
		self.settings = MagicMock()
		self.settings.metal_issuer_id = TEST_ISSUER
		self.settings.get_password.return_value = self.private_key
		self.issuer = MetalTokenIssuer(self.settings)

	def issue(self, **overrides) -> str:
		request = {
			"virtual_machine_id": "vm-00001",
			"caller": TEST_CALLER,
			"receiver": TEST_RECEIVER,
			"scopes": [SCOPE_READ_VIRTUAL_MACHINE, SCOPE_MIGRATION],
		}
		return self.issuer.issue_token(**(request | overrides))

	def test_the_token_carries_the_agreed_claims(self) -> None:
		token = self.issue()

		claims = jwt.decode(
			token,
			load_pem_private_key(self.private_key.encode(), password=None).public_key(),
			algorithms=["EdDSA"],
			audience=TEST_RECEIVER,
			issuer=TEST_ISSUER,
		)

		self.assertEqual(claims["vm_id"], "vm-00001")
		self.assertEqual(claims["sub"], TEST_CALLER)
		self.assertEqual(claims["aud"], TEST_RECEIVER)
		self.assertEqual(claims["scope"], [SCOPE_READ_VIRTUAL_MACHINE, SCOPE_MIGRATION])

	def test_the_header_names_the_signing_key(self) -> None:
		header = jwt.get_unverified_header(self.issue())

		self.assertEqual(header["alg"], "EdDSA")
		self.assertEqual(header["kid"], get_key_id(self.private_key))

	def test_the_token_expires_inside_the_requested_window(self) -> None:
		token = self.issue(expires_in=timedelta(minutes=30))

		expiry = datetime.fromtimestamp(jwt.decode(token, options={"verify_signature": False})["exp"], UTC)

		self.assertLessEqual(expiry, datetime.now(UTC) + timedelta(minutes=30))
		self.assertGreater(expiry, datetime.now(UTC) + timedelta(minutes=29))

	def test_an_expiry_outside_two_hours_is_refused(self) -> None:
		with self.assertRaises(MetalTokenError):
			self.issue(expires_in=timedelta(hours=3))

		with self.assertRaises(MetalTokenError):
			self.issue(expires_in=timedelta(0))

	def test_an_unknown_scope_is_refused(self) -> None:
		with self.assertRaises(MetalTokenError):
			self.issue(scopes=[SCOPE_MIGRATION, "host_admin"])

	def test_a_token_without_a_scope_is_refused(self) -> None:
		with self.assertRaises(MetalTokenError):
			self.issue(scopes=[])

	def test_a_token_without_a_receiver_is_refused(self) -> None:
		with self.assertRaises(MetalTokenError):
			self.issue(receiver="")

	def test_a_missing_signing_key_is_refused(self) -> None:
		self.settings.get_password.return_value = None

		with self.assertRaises(MetalTokenError):
			self.issue()


class TestPublicKeySync(UnitTestCase):
	def setUp(self) -> None:
		self.current_key = generate_private_key()
		self.previous_key = generate_private_key()
		self.settings = MagicMock()
		self.settings.metal_issuer_id = TEST_ISSUER
		self.settings.metal_token_key_rotated_on = now_datetime()
		self.settings.get_password.side_effect = self.get_password

	def get_password(self, fieldname: str, **_: object) -> str | None:
		return {
			"metal_token_private_key": self.current_key,
			"previous_metal_token_private_key": self.previous_key,
		}.get(fieldname)

	def test_a_recent_rotation_publishes_both_keys(self) -> None:
		keys = MetalTokenIssuer(self.settings).get_public_keys()

		self.assertEqual(
			[key["id"] for key in keys],
			[get_key_id(self.current_key), get_key_id(self.previous_key)],
		)
		self.assertEqual(keys[0]["key"], get_public_key(self.current_key))

	def test_the_previous_key_is_dropped_after_the_overlap(self) -> None:
		self.settings.metal_token_key_rotated_on = add_to_date(now_datetime(), hours=-3)

		keys = MetalTokenIssuer(self.settings).get_public_keys()

		self.assertEqual([key["id"] for key in keys], [get_key_id(self.current_key)])

	def test_a_host_without_a_previous_key_gets_one_key(self) -> None:
		self.previous_key = None

		keys = MetalTokenIssuer(self.settings).get_public_keys()

		self.assertEqual([key["id"] for key in keys], [get_key_id(self.current_key)])

	def test_the_payload_names_the_issuer_and_the_receiver(self) -> None:
		payload = MetalTokenIssuer(self.settings).get_key_sync_payload(TEST_RECEIVER)

		self.assertEqual(payload["issuer"], TEST_ISSUER)
		self.assertEqual(payload["receiver"], TEST_RECEIVER)
		self.assertEqual(len(payload["public_keys"]), 2)
