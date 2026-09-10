# Copyright (c) 2026, Frappe and Contributors
# See license.txt

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import load_pem_private_key
from frappe.tests import UnitTestCase

from atlas.metal_server.core.metal_token import generate_private_key


class TestGeneratePrivateKey(UnitTestCase):
	def test_the_key_is_a_usable_ed25519_key(self) -> None:
		private_key = load_pem_private_key(generate_private_key().encode(), password=None)

		self.assertIsInstance(private_key, Ed25519PrivateKey)

	def test_every_key_is_new(self) -> None:
		self.assertNotEqual(generate_private_key(), generate_private_key())
