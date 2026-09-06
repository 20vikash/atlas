from __future__ import annotations

import tomllib
from unittest.mock import patch

import bcrypt
from frappe.tests import UnitTestCase

from atlas.service.core.configuration import APPLY_COMMAND, CONFIG_PATH, ProxyConfiguration

CERTIFICATE = "-----BEGIN CERTIFICATE-----\nleaf\n-----END CERTIFICATE-----"
PRIVATE_KEY = "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----"


class _FakeSettings:
	def __init__(self) -> None:
		self.wildcard_domain = "par-1.example.com"
		self.proxy_jwks_url = "https://issuer.example.com/jwks.json"
		self.proxy_jwks_audience_id = "atlas-proxy-control"
		self.wildcard_tls_expires_on = None
		self.passwords = {
			"wildcard_tls_certificate": CERTIFICATE,
			"wildcard_tls_private_key": PRIVATE_KEY,
		}

	def get_password(self, fieldname: str, raise_exception: bool = True) -> str | None:
		return self.passwords.get(fieldname)


class _FakeProxyServer:
	name = "proxy-001"

	def __init__(self, password: str = "a-control-password") -> None:
		self.password = password

	def get_password(self, fieldname: str, raise_exception: bool = True) -> str | None:
		return self.password


def _build(settings: _FakeSettings | None = None, password: str = "a-control-password"):
	patched = patch(
		"atlas.service.core.configuration.frappe.get_single", return_value=settings or _FakeSettings()
	)
	patched.start()
	configuration = ProxyConfiguration(_FakeProxyServer(password))
	patched.stop()
	return configuration


class TestProxyConfiguration(UnitTestCase):
	def test_the_file_is_valid_toml_with_every_section(self) -> None:
		document = tomllib.loads(_build().content)

		self.assertEqual(document["tls"]["wildcard_domain"], "*.par-1.example.com")
		self.assertEqual(document["tls"]["enabled"], True)
		self.assertEqual(document["tls"]["fullchain_pem"].strip(), CERTIFICATE)
		self.assertEqual(document["tls"]["private_key_pem"].strip(), PRIVATE_KEY)
		self.assertEqual(document["auth"]["jwks_audience_id"], "atlas-proxy-control")

	# Atlas keeps the password and the proxy keeps the hash.
	def test_the_file_carries_a_hash_and_never_the_password(self) -> None:
		configuration = _build(password="a-control-password")

		content = configuration.content

		document = tomllib.loads(content)
		self.assertNotIn("a-control-password", content)
		self.assertTrue(bcrypt.checkpw(b"a-control-password", document["auth"]["password_hash"].encode()))

	def test_a_missing_certificate_is_refused(self) -> None:
		settings = _FakeSettings()
		settings.passwords = {}

		with self.assertRaises(Exception):
			_build(settings).content

	def test_a_missing_control_password_is_refused(self) -> None:
		with self.assertRaises(Exception):
			_build(password="").content

	# The hash is salted, so the file differs every time. The digest must not.
	def test_the_digest_is_stable_while_the_inputs_are(self) -> None:
		configuration = _build()

		self.assertNotEqual(configuration.content, configuration.content)
		self.assertEqual(configuration.digest, configuration.digest)

	def test_the_digest_follows_a_changed_certificate(self) -> None:
		before = _build().digest

		settings = _FakeSettings()
		settings.passwords["wildcard_tls_certificate"] = "-----BEGIN CERTIFICATE-----\nnew\n-----"

		self.assertNotEqual(before, _build(settings).digest)

	def test_the_digest_follows_a_changed_password(self) -> None:
		before = _build(password="first").digest

		self.assertNotEqual(before, _build(password="second").digest)

	def test_the_push_command_writes_the_file_and_applies_it(self) -> None:
		command = _build().get_push_command()

		self.assertIn(f"install -m 0600 /dev/null {CONFIG_PATH}", command)
		self.assertIn(f"cat > {CONFIG_PATH} <<'ATLAS_PROXY_CONFIG_END'", command)
		self.assertIn(APPLY_COMMAND, command)
		# The apply step must run after the heredoc closes, not inside it.
		self.assertLess(command.rindex("ATLAS_PROXY_CONFIG_END"), command.index(f"\n{APPLY_COMMAND}"))


class TestPushToActiveProxies(UnitTestCase):
	def test_only_active_proxies_are_queued(self) -> None:
		from atlas.service.core import configuration

		with (
			patch.object(configuration.frappe, "get_all", return_value=["proxy-001"]) as get_all,
			patch.object(configuration.frappe, "enqueue_doc") as enqueue_doc,
		):
			configuration.push_configuration_to_active_proxies()

		self.assertEqual(get_all.call_args.kwargs["filters"], {"status": "Active"})
		self.assertEqual(enqueue_doc.call_args.args[:2], ("Proxy Server", "proxy-001"))
		self.assertEqual(enqueue_doc.call_args.args[2], "_push_configuration")
