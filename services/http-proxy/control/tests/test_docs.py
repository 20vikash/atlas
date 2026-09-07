import importlib
import sys
from pathlib import Path

import pytest
from fastapi.testclient import TestClient


def _client(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
	"""Import the app with a configuration file the test controls."""
	path = tmp_path / "proxy-control.toml"
	path.write_text(
		"""[control]
domain = "proxy-001.par-1.example.com"

[tls]
wildcard_domain = "*.par-1.example.com"
fullchain_pem = "leaf"
private_key_pem = "key"
"""
	)
	monkeypatch.setenv("ATLAS_PROXY_CONTROL_CONFIG", str(path))
	sys.modules.pop("proxy_control.main", None)
	return TestClient(importlib.import_module("proxy_control.main").app)


def test_the_reference_page_and_the_schema_are_open(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
	client = _client(tmp_path, monkeypatch)

	assert client.get("/docs").status_code == 200
	assert client.get("/docs/swagger.json").status_code == 200
	assert client.get("/v1/state").status_code == 401


def test_the_schema_lists_the_mapping_routes(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
	client = _client(tmp_path, monkeypatch)

	schema = client.get("/docs/swagger.json").json()
	paths = schema["paths"]

	assert "/v1/state" in paths
	assert sorted(paths["/v1/sites/{name}"]) == ["delete", "patch"]
	assert sorted(paths["/v1/domains/{domain}"]) == ["delete", "patch"]
	assert "/docs" not in paths
	assert schema["components"]["securitySchemes"]["BearerAuth"]["scheme"] == "bearer"

	for path, methods in paths.items():
		for operation in methods.values():
			assert operation["summary"]
			assert operation["description"]
			if path != "/healthz":
				assert operation["security"] == [{"BearerAuth": []}]

	assert paths["/v1/sites"]["put"]["summary"] == "Sync site routes"
	assert "erp" in paths["/v1/sites/{name}"]["patch"]["description"]


def test_the_state_response_names_its_fields(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
	client = _client(tmp_path, monkeypatch)

	schema = client.get("/docs/swagger.json").json()["components"]["schemas"]["State"]

	assert sorted(schema["properties"]) == ["domains", "sites"]
	assert schema["properties"]["sites"]["examples"] == [{"erp": "2001:db8::10", "shop": "2001:db8::11"}]


def test_the_reference_does_not_persist_credentials(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
	page = _client(tmp_path, monkeypatch).get("/docs").text

	assert "persistAuth: false" in page
	assert 'preferredSecurityScheme: "BearerAuth"' in page
	assert 'defaultHttpClient: { targetKey: "shell", clientKey: "curl" }' in page
	assert "hiddenClients" not in page
