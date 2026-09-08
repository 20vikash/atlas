from __future__ import annotations

import base64
import hashlib
import json
from contextlib import contextmanager
from unittest.mock import MagicMock, patch

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.utils import encode_dss_signature
from frappe.tests import UnitTestCase

from atlas.atlas.core.tls.acme import AcmeClient, AcmeError, Order

DIRECTORY = {
	"newNonce": "https://acme.test/nonce",
	"newAccount": "https://acme.test/account",
	"newOrder": "https://acme.test/order",
}


def _response(status_code: int = 200, body: dict | None = None, headers: dict | None = None):
	response = MagicMock()
	response.status_code = status_code
	response.headers = {"Replay-Nonce": "nonce-from-response", **(headers or {})}
	response.text = json.dumps(body) if body is not None else ""
	response.json.return_value = body if body is not None else {}
	return response


def _build_client() -> AcmeClient:
	client = AcmeClient("https://acme.test/directory", ec.generate_private_key(ec.SECP256R1()))
	client._directory = dict(DIRECTORY)
	client.POLL_INTERVAL = 0
	return client


@contextmanager
def _patched_requests():
	"""Patch only the request functions, so the real requests exceptions stay usable."""
	with (
		patch("atlas.atlas.core.tls.acme.requests.post") as post,
		patch("atlas.atlas.core.tls.acme.requests.head") as head,
	):
		head.return_value = _response(200, headers={"Replay-Nonce": "nonce-from-head"})
		yield post, head


def _decode(value: str) -> bytes:
	return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))


def _protected_header(post: MagicMock, call_index: int = 0) -> dict:
	body = json.loads(post.call_args_list[call_index].kwargs["data"])
	return json.loads(_decode(body["protected"]))


class TestAcmeClient(UnitTestCase):
	def test_register_account_signs_with_the_key_and_returns_the_account_url(self) -> None:
		client = _build_client()
		with _patched_requests() as (post, _head):
			post.return_value = _response(
				201, {"status": "valid"}, {"Location": "https://acme.test/account/1"}
			)

			account_url = client.register_account("owner@example.com")

			header = _protected_header(post)

		self.assertEqual(account_url, "https://acme.test/account/1")
		self.assertEqual(client.account_url, "https://acme.test/account/1")
		self.assertEqual(header["alg"], "ES256")
		self.assertIn("jwk", header)
		self.assertNotIn("kid", header)

	def test_a_registered_client_signs_with_the_key_identifier(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		with _patched_requests() as (post, _head):
			post.return_value = _response(
				201,
				{"status": "pending", "authorizations": ["https://acme.test/authz/1"], "finalize": "f"},
				{"Location": "https://acme.test/order/1"},
			)

			client.create_order(["*.example.com"])

			header = _protected_header(post)

		self.assertEqual(header["kid"], "https://acme.test/account/1")
		self.assertNotIn("jwk", header)

	def test_the_signature_verifies_against_the_account_key(self) -> None:
		client = _build_client()
		with _patched_requests() as (post, _head):
			post.return_value = _response(201, {}, {"Location": "https://acme.test/account/1"})

			client.register_account("owner@example.com")

			body = json.loads(post.call_args.kwargs["data"])

		signature = _decode(body["signature"])
		self.assertEqual(len(signature), 64)
		client.account_key.public_key().verify(
			encode_dss_signature(
				int.from_bytes(signature[:32], "big"), int.from_bytes(signature[32:], "big")
			),
			f"{body['protected']}.{body['payload']}".encode(),
			ec.ECDSA(hashes.SHA256()),
		)

	def test_post_as_get_sends_an_empty_payload(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		with _patched_requests() as (post, _head):
			post.return_value = _response(200, {"status": "valid"})

			client._post_as_get("https://acme.test/authz/1")

			body = json.loads(post.call_args.kwargs["data"])

		self.assertEqual(body["payload"], "")

	def test_record_value_is_the_digest_of_the_key_authorization(self) -> None:
		client = _build_client()

		value = client.get_record_value("token-1")

		thumbprint = client._thumbprint
		expected = hashlib.sha256(f"token-1.{thumbprint}".encode()).digest()
		self.assertEqual(value, base64.urlsafe_b64encode(expected).rstrip(b"=").decode())
		self.assertNotIn("=", value)

	def test_get_dns_challenge_uses_the_zone_of_a_wildcard_identifier(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		authorization = {
			"identifier": {"type": "dns", "value": "*.example.com"},
			"status": "pending",
			"challenges": [
				{"type": "http-01", "url": "https://acme.test/challenge/http", "token": "other"},
				{"type": "dns-01", "url": "https://acme.test/challenge/dns", "token": "token-1"},
			],
		}
		with _patched_requests() as (post, _head):
			post.return_value = _response(200, authorization)

			challenge = client.get_dns_challenge("https://acme.test/authz/1")

		self.assertEqual(challenge.challenge_url, "https://acme.test/challenge/dns")
		self.assertEqual(challenge.record_name, "_acme-challenge.example.com")
		self.assertEqual(challenge.record_value, client.get_record_value("token-1"))

	def test_get_dns_challenge_fails_when_the_server_offers_no_dns_challenge(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		authorization = {
			"identifier": {"type": "dns", "value": "example.com"},
			"status": "pending",
			"challenges": [{"type": "http-01", "url": "https://acme.test/challenge/http", "token": "t"}],
		}
		with _patched_requests() as (post, _head):
			post.return_value = _response(200, authorization)

			with self.assertRaises(AcmeError):
				client.get_dns_challenge("https://acme.test/authz/1")

	def test_a_nonce_is_fetched_once_and_then_read_from_each_response(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		with _patched_requests() as (post, head):
			post.return_value = _response(200, {"status": "valid"})

			client._post_as_get("https://acme.test/authz/1")
			client._post_as_get("https://acme.test/authz/1")

			head.assert_called_once()
			self.assertEqual(_protected_header(post, 0)["nonce"], "nonce-from-head")
			self.assertEqual(_protected_header(post, 1)["nonce"], "nonce-from-response")

	def test_a_rejected_nonce_is_retried_once(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		rejected = _response(400, {"type": "urn:ietf:params:acme:error:badNonce", "detail": "bad nonce"})
		with _patched_requests() as (post, _head):
			post.side_effect = [rejected, _response(200, {"status": "valid"})]

			client._post_as_get("https://acme.test/authz/1")

			self.assertEqual(post.call_count, 2)

	def test_a_rejected_request_raises_with_the_server_detail(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		with _patched_requests() as (post, _head):
			post.return_value = _response(403, {"type": "unauthorized", "detail": "no access"})

			with self.assertRaises(AcmeError) as raised:
				client._post_as_get("https://acme.test/authz/1")

		self.assertIn("no access", str(raised.exception))

	def test_wait_for_authorization_fails_on_an_invalid_authorization(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		with _patched_requests() as (post, _head):
			post.return_value = _response(200, {"status": "invalid"})

			with self.assertRaises(AcmeError) as raised:
				client.wait_for_authorization("https://acme.test/authz/1")

		self.assertIn("invalid", str(raised.exception))

	def test_finalize_order_sends_the_request_and_returns_the_certificate_url(self) -> None:
		client = _build_client()
		client.account_url = "https://acme.test/account/1"
		order = Order(
			url="https://acme.test/order/1",
			authorization_urls=["https://acme.test/authz/1"],
			finalize_url="https://acme.test/order/1/finalize",
		)
		with _patched_requests() as (post, _head):
			post.side_effect = [
				_response(200, {"status": "processing"}),
				_response(200, {"status": "valid", "certificate": "https://acme.test/cert/1"}),
			]

			certificate_url = client.finalize_order(order, b"csr-bytes")

			payload = json.loads(post.call_args_list[0].kwargs["data"])["payload"]

		self.assertEqual(certificate_url, "https://acme.test/cert/1")
		self.assertEqual(
			json.loads(_decode(payload)),
			{"csr": base64.urlsafe_b64encode(b"csr-bytes").rstrip(b"=").decode()},
		)
