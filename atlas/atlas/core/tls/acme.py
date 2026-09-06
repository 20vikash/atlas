from __future__ import annotations

import base64
import hashlib
import json
import time
from dataclasses import dataclass
from typing import Any

import requests
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.utils import decode_dss_signature

JOSE_CONTENT_TYPE = "application/jose+json"
CERTIFICATE_CONTENT_TYPE = "application/pem-certificate-chain"


class AcmeError(Exception):
	"""Raised when the ACME server rejects a request or an order does not become valid."""


@dataclass(frozen=True)
class Order:
	"""One ACME order and the URLs it exposes."""

	url: str
	authorization_urls: list[str]
	finalize_url: str


@dataclass(frozen=True)
class DnsChallenge:
	"""One dns-01 challenge and the TXT record that answers it."""

	challenge_url: str
	record_name: str
	record_value: str


class AcmeClient:
	"""Speak ACME v2 for one account key."""

	POLL_INTERVAL = 3
	POLL_TIMEOUT = 180

	def __init__(
		self,
		directory_url: str,
		account_key: ec.EllipticCurvePrivateKey,
		request_timeout: int = 30,
	) -> None:
		self.directory_url = directory_url
		self.account_key = account_key
		self.request_timeout = request_timeout
		self.account_url: str | None = None
		self._directory: dict[str, Any] | None = None
		self._replay_nonce: str | None = None

	@property
	def directory(self) -> dict[str, Any]:
		"""Return the cached ACME directory document."""
		if self._directory is None:
			response = requests.get(self.directory_url, timeout=self.request_timeout)
			self._directory = self._json(response, "fetch the ACME directory")
		return self._directory

	def register_account(self, email: str) -> str:
		"""Create or reuse the account for the key."""
		payload = {"termsOfServiceAgreed": True, "contact": [f"mailto:{email}"]}
		response = self._post(self.directory["newAccount"], payload)
		account_url = response.headers.get("Location")
		if not account_url:
			raise AcmeError("ACME newAccount response carried no account URL.")

		self.account_url = account_url
		return account_url

	def create_order(self, identifiers: list[str]) -> Order:
		"""Create an order for the given DNS identifiers."""
		payload = {"identifiers": [{"type": "dns", "value": name} for name in identifiers]}
		response = self._post(self.directory["newOrder"], payload)
		order_url = response.headers.get("Location")
		if not order_url:
			raise AcmeError("ACME newOrder response carried no order URL.")

		body = self._json(response, "create an ACME order")
		return Order(
			url=order_url,
			authorization_urls=body["authorizations"],
			finalize_url=body["finalize"],
		)

	def get_dns_challenge(self, authorization_url: str) -> DnsChallenge:
		"""Return the dns-01 challenge of one authorization."""
		authorization = self._json(self._post_as_get(authorization_url), "read an ACME authorization")
		for challenge in authorization.get("challenges", []):
			if challenge.get("type") != "dns-01":
				continue

			domain = authorization["identifier"]["value"].removeprefix("*.")
			return DnsChallenge(
				challenge_url=challenge["url"],
				record_name=f"_acme-challenge.{domain}",
				record_value=self.get_record_value(challenge["token"]),
			)

		raise AcmeError(f"Authorization {authorization_url} offers no dns-01 challenge.")

	def get_record_value(self, token: str) -> str:
		"""Return the TXT value that proves control for the challenge token."""
		key_authorization = f"{token}.{self._thumbprint}".encode()
		return _base64url(hashlib.sha256(key_authorization).digest())

	def submit_challenge(self, challenge_url: str) -> None:
		"""Tell the server that the TXT record is in place."""
		self._post(challenge_url, {})

	def wait_for_authorization(self, authorization_url: str) -> None:
		"""Wait for an authorization to become valid."""
		authorization = self._wait(authorization_url, "read an ACME authorization")
		if authorization["status"] != "valid":
			raise AcmeError(f"Authorization {authorization_url} is {authorization['status']}.")

	def finalize_order(self, order: Order, certificate_request: bytes) -> str:
		"""Finalize an order and return its certificate URL."""
		self._post(order.finalize_url, {"csr": _base64url(certificate_request)})
		finalized = self._wait(order.url, "read an ACME order")
		if finalized["status"] != "valid":
			raise AcmeError(f"Order {order.url} is {finalized['status']}.")

		return finalized["certificate"]

	def download_certificate(self, certificate_url: str) -> str:
		"""Return the issued certificate chain in PEM format."""
		return self._post_as_get(certificate_url, accept=CERTIFICATE_CONTENT_TYPE).text

	@property
	def _thumbprint(self) -> str:
		"""Return the RFC 7638 thumbprint of the account key."""
		key = json.dumps(self._jwk, sort_keys=True, separators=(",", ":")).encode()
		return _base64url(hashlib.sha256(key).digest())

	@property
	def _jwk(self) -> dict[str, str]:
		numbers = self.account_key.public_key().public_numbers()
		size = (self.account_key.curve.key_size + 7) // 8
		return {
			"crv": "P-256",
			"kty": "EC",
			"x": _base64url(numbers.x.to_bytes(size, "big")),
			"y": _base64url(numbers.y.to_bytes(size, "big")),
		}

	def _wait(self, url: str, action: str) -> dict[str, Any]:
		"""Poll a resource until it leaves the pending and processing states."""
		deadline = time.monotonic() + self.POLL_TIMEOUT
		while True:
			body = self._json(self._post_as_get(url), action)
			if body["status"] not in ("pending", "processing", "ready"):
				return body
			if time.monotonic() >= deadline:
				raise AcmeError(f"{url} stayed {body['status']} for {self.POLL_TIMEOUT} seconds.")

			time.sleep(self.POLL_INTERVAL)

	def _post_as_get(self, url: str, accept: str | None = None) -> requests.Response:
		return self._post(url, None, accept=accept)

	def _post(self, url: str, payload: dict[str, Any] | None, accept: str | None = None) -> requests.Response:
		"""Send one signed request and retry a rejected nonce once."""
		response = self._send(url, payload, accept)
		if (
			response.status_code >= 400
			and self._error_type(response) == "urn:ietf:params:acme:error:badNonce"
		):
			response = self._send(url, payload, accept)

		self._raise_for_status(response, f"post to {url}")
		return response

	def _send(self, url: str, payload: dict[str, Any] | None, accept: str | None) -> requests.Response:
		headers = {"Content-Type": JOSE_CONTENT_TYPE}
		if accept:
			headers["Accept"] = accept

		try:
			response = requests.post(
				url,
				data=json.dumps(self._sign(url, payload)),
				headers=headers,
				timeout=self.request_timeout,
			)
		except requests.RequestException as error:
			raise AcmeError(f"ACME request to {url} failed: {error}") from error

		self._replay_nonce = response.headers.get("Replay-Nonce")
		return response

	def _sign(self, url: str, payload: dict[str, Any] | None) -> dict[str, str]:
		"""Return the signed request body."""
		protected: dict[str, Any] = {"alg": "ES256", "nonce": self._take_nonce(), "url": url}
		if self.account_url:
			protected["kid"] = self.account_url
		else:
			protected["jwk"] = self._jwk

		encoded_protected = _base64url(json.dumps(protected).encode())
		encoded_payload = "" if payload is None else _base64url(json.dumps(payload).encode())
		signature = self.account_key.sign(
			f"{encoded_protected}.{encoded_payload}".encode(), ec.ECDSA(hashes.SHA256())
		)

		return {
			"protected": encoded_protected,
			"payload": encoded_payload,
			"signature": _base64url(_raw_signature(signature, self.account_key.curve.key_size)),
		}

	def _take_nonce(self) -> str:
		"""Return an unused nonce. Every response supplies the next one."""
		if self._replay_nonce is None:
			response = requests.head(self.directory["newNonce"], timeout=self.request_timeout)
			self._replay_nonce = response.headers.get("Replay-Nonce")
			if not self._replay_nonce:
				raise AcmeError("ACME newNonce response carried no nonce.")

		nonce = self._replay_nonce
		self._replay_nonce = None
		return nonce

	def _json(self, response: requests.Response, action: str) -> dict[str, Any]:
		self._raise_for_status(response, action)
		try:
			return response.json()
		except ValueError as error:
			raise AcmeError(f"Unable to {action}: the response was not JSON.") from error

	def _raise_for_status(self, response: requests.Response, action: str) -> None:
		if response.status_code < 400:
			return

		detail = self._error_detail(response) or response.text[:200]
		raise AcmeError(f"Unable to {action}: {response.status_code} {detail}")

	@staticmethod
	def _error_type(response: requests.Response) -> str:
		try:
			return response.json().get("type", "")
		except ValueError:
			return ""

	@staticmethod
	def _error_detail(response: requests.Response) -> str:
		try:
			body = response.json()
		except ValueError:
			return ""
		return body.get("detail", "")


def _base64url(data: bytes) -> str:
	"""Return unpadded base64url, which is the only encoding JOSE accepts."""
	return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def _raw_signature(der_signature: bytes, key_size: int) -> bytes:
	"""Convert a DER ECDSA signature to the fixed-width R and S pair that ES256 needs."""
	r, s = decode_dss_signature(der_signature)
	size = (key_size + 7) // 8
	return r.to_bytes(size, "big") + s.to_bytes(size, "big")
