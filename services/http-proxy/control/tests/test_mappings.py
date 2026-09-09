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


# A full replacement must not hide an invalid controller route.
def test_a_full_replace_rejects_the_reserved_key():
	store, client = _store()

	with pytest.raises(HTTPException) as raised:
		asyncio.run(store.replace("sites", {"erp": "2001:db8::1", "proxy-001": "2001:db8::2"}))

	assert raised.value.status_code == 409
	assert client.requests == []


# Only a site subdomain can collide. A custom domain is a complete name.
def test_a_custom_domain_of_the_same_name_is_allowed():
	store, client = _store()

	asyncio.run(store.update("domains", "proxy-001", "2001:db8::1"))

	assert client.requests[0][1] == "/v1/domains/proxy-001"


def test_proxy_prefix_is_reserved_without_a_control_domain():
	store, client = _store(reserved="")

	with pytest.raises(HTTPException):
		asyncio.run(store.update("sites", "proxy-001", "2001:db8::1"))

	assert client.requests == []


def test_proxy_name_is_reserved_without_a_control_domain():
	store, client = _store(reserved="")

	with pytest.raises(HTTPException):
		asyncio.run(store.update("sites", "proxy", "2001:db8::1"))

	assert client.requests == []


def test_wildcard_subdomain_is_rejected_from_domains():
	client = _RecordingClient()
	store = MappingStore(client, wildcard_domain="*.par-1.example.com")

	with pytest.raises(HTTPException) as raised:
		asyncio.run(store.update("domains", "shop.par-1.example.com", "2001:db8::1"))

	assert raised.value.status_code == 409
	assert client.requests == []


def test_external_custom_domain_is_allowed():
	client = _RecordingClient()
	store = MappingStore(client, wildcard_domain="*.par-1.example.com")

	asyncio.run(store.update("domains", "shop.example.net", "2001:db8::1"))

	assert client.requests[0][1] == "/v1/domains/shop.example.net"


def test_a_custom_domain_wildcard_is_refused():
	store, client = _store()

	with pytest.raises(HTTPException) as raised:
		asyncio.run(store.update("domains", "*-shop.example.com", "2001:db8::1"))

	assert raised.value.status_code == 422
	assert client.requests == []


@pytest.mark.parametrize("key", ["site-2avaxbsw", "site-admin", "bench-vm-2avaxbsw", "bench-vm-admin"])
def test_an_auto_proxy_prefix_is_refused(key: str):
	client = _RecordingClient()
	store = MappingStore(client, auto_proxy_host_prefixes=("site-", "*-vm-"))

	with pytest.raises(HTTPException) as raised:
		asyncio.run(store.update("sites", key, "2001:db8::1"))

	assert raised.value.status_code == 409
	assert client.requests == []


def test_a_full_replace_rejects_an_auto_proxy_prefix():
	client = _RecordingClient()
	store = MappingStore(client, auto_proxy_host_prefixes=("site-", "*-vm-"))

	with pytest.raises(HTTPException):
		asyncio.run(store.replace("sites", {"erp": "2001:db8::1", "bench-vm-admin": "2001:db8::2"}))

	assert client.requests == []


def test_an_auto_proxy_name_is_allowed_as_a_custom_domain():
	client = _RecordingClient()
	store = MappingStore(client, auto_proxy_host_prefixes=("site-", "*-vm-"))

	asyncio.run(store.update("domains", "site-2avaxbsw", "2001:db8::1"))

	assert client.requests[0][1] == "/v1/domains/site-2avaxbsw"


def test_no_configured_prefix_leaves_every_site_key_usable():
	client = _RecordingClient()
	store = MappingStore(client)

	asyncio.run(store.update("sites", "site-2avaxbsw", "2001:db8::1"))

	assert client.requests[0][1] == "/v1/sites/site-2avaxbsw"
