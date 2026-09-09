"""Development JWKS server. Serves one RSA key set and mints a token for each audience you type."""

from __future__ import annotations

import argparse
import base64
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

KEY_PATH = Path(__file__).parent / ".dev-jwks-key.pem"
KEY_ID = "dev"
ISSUER = "atlas-dev-jwks"
SUBJECT = "dev@atlas.local"
LIFETIME = 24 * 60 * 60


def load_private_key() -> rsa.RSAPrivateKey:
	"""Return the stored signing key, and create it on first use."""
	if not KEY_PATH.exists():
		key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
		KEY_PATH.write_bytes(
			key.private_bytes(
				encoding=serialization.Encoding.PEM,
				format=serialization.PrivateFormat.PKCS8,
				encryption_algorithm=serialization.NoEncryption(),
			)
		)
		KEY_PATH.chmod(0o600)

	return serialization.load_pem_private_key(KEY_PATH.read_bytes(), password=None)


PRIVATE_KEY = load_private_key()


def to_base64url(value: int) -> str:
	raw = value.to_bytes((value.bit_length() + 7) // 8, "big")
	return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def get_key_set() -> dict:
	numbers = PRIVATE_KEY.public_key().public_numbers()
	return {
		"keys": [
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": KEY_ID,
				"n": to_base64url(numbers.n),
				"e": to_base64url(numbers.e),
			}
		]
	}


def mint_token(audience: str) -> str:
	now = int(time.time())
	claims = {
		"iss": ISSUER,
		"sub": SUBJECT,
		"aud": audience,
		"iat": now,
		"exp": now + LIFETIME,
	}
	return jwt.encode(claims, PRIVATE_KEY, algorithm="RS256", headers={"kid": KEY_ID})


class Handler(BaseHTTPRequestHandler):
	def do_GET(self) -> None:
		body = json.dumps(get_key_set(), indent=2).encode()
		self.send_response(200)
		self.send_header("Content-Type", "application/json")
		self.send_header("Content-Length", str(len(body)))
		self.end_headers()
		self.wfile.write(body)

	def log_message(self, format: str, *args) -> None:
		pass


def prompt_for_tokens() -> None:
	"""Mint one token for each audience typed, until the user exits."""
	while True:
		try:
			audience = input("audience id> ").strip()
		except EOFError, KeyboardInterrupt:
			print()
			return

		if audience in ("exit", "quit"):
			return

		if audience:
			print(f"\n{mint_token(audience)}\n")


def main() -> None:
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--host", default="127.0.0.1")
	parser.add_argument("--port", type=int, default=8787)
	arguments = parser.parse_args()

	server = ThreadingHTTPServer((arguments.host, arguments.port), Handler)
	threading.Thread(target=server.serve_forever, daemon=True).start()

	print(f"JWKS URL: http://{arguments.host}:{arguments.port}/jwks")
	print("Type an audience id for a token valid 1 day. Type exit or press Ctrl+C to stop.\n")
	prompt_for_tokens()
	server.shutdown()


if __name__ == "__main__":
	main()
