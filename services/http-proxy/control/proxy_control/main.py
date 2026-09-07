import asyncio
from contextlib import asynccontextmanager
from typing import Annotated

import httpx
from fastapi import Body, Depends, FastAPI, Path, Response, status
from pydantic import BaseModel, ConfigDict, Field

from . import docs
from .auth import Authentication
from .client import ProxyClient
from .config import ConfigError, load
from .mappings import MappingStore
from .server import run

SITE_MAP_EXAMPLE = {"erp": "2001:db8::10", "shop": "2001:db8::11"}
DOMAIN_MAP_EXAMPLE = {"www.example.com": "2001:db8::20"}


class AddressUpdate(BaseModel):
	"""One address for one site or custom domain."""

	model_config = ConfigDict(extra="forbid")

	address: str = Field(
		min_length=1,
		examples=["2001:db8::10"],
		description="The backend IPv6 address. Use `-` only for a site to stop its traffic.",
	)


class Health(BaseModel):
	"""Daemon health."""

	ok: bool


class State(BaseModel):
	"""The site map and the custom-domain map."""

	model_config = ConfigDict(
		json_schema_extra={"example": {"sites": SITE_MAP_EXAMPLE, "domains": DOMAIN_MAP_EXAMPLE}}
	)

	sites: dict[str, str] = Field(
		examples=[SITE_MAP_EXAMPLE],
		description="Site subdomains and their backend addresses.",
	)
	domains: dict[str, str] = Field(
		examples=[DOMAIN_MAP_EXAMPLE],
		description="Custom domains and their backend addresses.",
	)


class MapReplaced(BaseModel):
	"""Result of a full map replacement."""

	synced: bool = Field(description="Whether the complete map was applied.")
	entries: int = Field(description="Number of entries in the replacement map.")


class SiteMapping(BaseModel):
	"""One site as the proxy stored it."""

	site: str = Field(description="The site subdomain.")
	address: str = Field(description="The backend IPv6 address.")


class DomainMapping(BaseModel):
	"""One custom domain as the proxy stored it."""

	domain: str = Field(description="The custom domain.")
	address: str = Field(description="The backend IPv6 address.")


try:
	_config = load()
except ConfigError as error:
	raise SystemExit(f"atlas-proxy-control: {error}") from error


auth = Authentication()
proxy = ProxyClient(_config.admin_socket)
maps = MappingStore(proxy, _config.reserved_subdomain)


@asynccontextmanager
async def lifespan(_: FastAPI):
	yield
	await proxy.close()


app = FastAPI(
	title="Atlas proxy control",
	description="Route sites and custom domains to backend IPv6 addresses. Sync all routes after a controller restart, or change one route when an address changes.",
	lifespan=lifespan,
	docs_url=None,
	redoc_url=None,
	openapi_url=None,
	openapi_tags=[
		{"name": "Health", "description": "Use these routes for liveness and readiness checks."},
		{"name": "Routing", "description": "Route sites and custom domains to backend IPv6 addresses."},
	],
)
protected = [Depends(auth.require_request)]


@app.get(
	"/healthz",
	tags=["Health"],
	summary="Check daemon health",
	description="Use this route for a liveness check. It does not check OpenResty.",
)
async def healthz() -> Health:
	return Health(ok=True)


@app.get(
	"/readyz",
	dependencies=protected,
	tags=["Health"],
	summary="Check OpenResty readiness",
	description="Use this route before a controller sends routing updates. It checks the OpenResty admin API.",
)
async def readyz() -> Response:
	try:
		response_status, _ = await proxy.request("GET", "/v1/healthz")
	except httpx.HTTPError:
		return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE)
	if response_status >= 300:
		return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE)
	return Response(status_code=status.HTTP_204_NO_CONTENT)


@app.get(
	"/v1/state",
	dependencies=protected,
	tags=["Routing"],
	summary="Read routes",
	description="Read the current routes during reconciliation. The response includes site and custom-domain maps.",
)
async def get_state() -> State:
	sites, domains = await asyncio.gather(maps.get("sites"), maps.get("domains"))
	return State(sites=sites, domains=domains)


@app.put(
	"/v1/sites",
	dependencies=protected,
	tags=["Routing"],
	summary="Sync site routes",
	description="Replace every site route after a controller restart or full reconciliation. Example: `erp` routes to `2001:db8::10`.",
)
async def replace_sites(
	values: Annotated[
		dict[str, str],
		Body(examples=[SITE_MAP_EXAMPLE], description="The complete desired site map."),
	],
) -> MapReplaced:
	return await maps.replace("sites", values)


@app.put(
	"/v1/domains",
	dependencies=protected,
	tags=["Routing"],
	summary="Sync domain routes",
	description="Replace every custom-domain route after a controller restart or full reconciliation. Example: `www.example.com` routes to `2001:db8::20`.",
)
async def replace_domains(
	values: Annotated[
		dict[str, str],
		Body(examples=[DOMAIN_MAP_EXAMPLE], description="The complete desired custom-domain map."),
	],
) -> MapReplaced:
	return await maps.replace("domains", values)


@app.patch(
	"/v1/sites/{name}",
	dependencies=protected,
	tags=["Routing"],
	summary="Route a site",
	description="Add or change one site route without changing other sites. Example: route `erp` to `2001:db8::10`.",
)
async def patch_site(
	name: Annotated[str, Path(description="The site subdomain.")], value: AddressUpdate
) -> SiteMapping:
	return await maps.update("sites", name, value.address)


@app.delete(
	"/v1/sites/{name}",
	dependencies=protected,
	tags=["Routing"],
	status_code=status.HTTP_204_NO_CONTENT,
	summary="Remove a site",
	description="Remove one site route when it no longer needs proxy traffic. This succeeds when the site is absent.",
)
async def delete_site(name: Annotated[str, Path(description="The site subdomain.")]) -> Response:
	await maps.delete("sites", name)
	return Response(status_code=status.HTTP_204_NO_CONTENT)


@app.patch(
	"/v1/domains/{domain}",
	dependencies=protected,
	tags=["Routing"],
	summary="Route a domain",
	description="Add or change one custom-domain route without changing other domains. Example: route `www.example.com` to `2001:db8::20`.",
)
async def patch_domain(
	domain: Annotated[str, Path(description="The complete custom domain.")], value: AddressUpdate
) -> DomainMapping:
	return await maps.update("domains", domain, value.address)


@app.delete(
	"/v1/domains/{domain}",
	dependencies=protected,
	tags=["Routing"],
	status_code=status.HTTP_204_NO_CONTENT,
	summary="Remove a domain",
	description="Remove one custom-domain route when it no longer needs proxy traffic. This succeeds when the domain is absent.",
)
async def delete_domain(domain: Annotated[str, Path(description="The complete custom domain.")]) -> Response:
	await maps.delete("domains", domain)
	return Response(status_code=status.HTTP_204_NO_CONTENT)


docs.add_routes(app)


if __name__ == "__main__":
	run(app)
