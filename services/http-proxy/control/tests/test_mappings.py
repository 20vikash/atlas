import asyncio

import pytest
from fastapi import HTTPException

from proxy_control.mappings import MappingStore


class _RecordingClient:
	"""Record every call the store forwards to OpenResty."""

	def __init__(self):
		self.requests = []

	async def request(self, method, path, body=None):
		self.requests.append((method, path, body))
		return 200, {}


def _store(reserved: str = "proxy-001") -> tuple[MappingStore, _RecordingClient]:
	client = _RecordingClient()
	return MappingStore(client, reserved), client


def test_patching_the_reserved_key_is_refused():
	store, client = _store()

	with pytest.raises(HTTPException) as raised:
		asyncio.run(store.update("sites", "proxy-001", "2001:db8::1"))

	assert raised.value.status_code == 409
	assert client.requests == []


def test_the_reserved_key_is_matched_without_case():
	store, client = _store()

	with pytest.raises(HTTPException):
		asyncio.run(store.delete("sites", "PROXY-001"))

	assert client.requests == []


# One bad key must not stop a controller resynchronizing every other site.
def test_a_full_replace_drops_the_reserved_key():
	store, client = _store()

	asyncio.run(store.replace("sites", {"erp": "2001:db8::1", "proxy-001": "2001:db8::2"}))

	assert client.requests == [("PUT", "/v1/sites", {"erp": "2001:db8::1"})]


# Only a site subdomain can collide. A custom domain is a complete name.
def test_a_custom_domain_of_the_same_name_is_allowed():
	store, client = _store()

	asyncio.run(store.update("domains", "proxy-001", "2001:db8::1"))

	assert client.requests[0][1] == "/v1/domains/proxy-001"


def test_nothing_is_reserved_without_a_control_domain():
	store, client = _store(reserved="")

	asyncio.run(store.update("sites", "proxy-001", "2001:db8::1"))

	assert client.requests[0][1] == "/v1/sites/proxy-001"
