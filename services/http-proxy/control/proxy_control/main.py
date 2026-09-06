import asyncio
from contextlib import asynccontextmanager

import httpx
from fastapi import Depends, FastAPI, Response, status
from pydantic import BaseModel, ConfigDict, Field

from .auth import Authentication
from .client import ProxyClient
from .config import ConfigError, load
from .mappings import MappingStore
from .server import run


class AddressUpdate(BaseModel):
	model_config = ConfigDict(extra="forbid")

	address: str = Field(min_length=1)


try:
	_config = load()
except ConfigError as error:
	raise SystemExit(f"atlas-proxy-control: {error}") from error


auth = Authentication()
proxy = ProxyClient(_config.admin_socket)
maps = MappingStore(proxy)


@asynccontextmanager
async def lifespan(_: FastAPI):
	yield
	await proxy.close()


app = FastAPI(title="Atlas proxy control", lifespan=lifespan)
protected = [Depends(auth.require)]


@app.get("/healthz")
async def healthz() -> dict[str, bool]:
	return {"ok": True}


@app.get("/readyz", dependencies=protected)
async def readyz() -> Response:
	try:
		response_status, _ = await proxy.request("GET", "/v1/healthz")
	except httpx.HTTPError:
		return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE)
	if response_status >= 300:
		return Response(status_code=status.HTTP_503_SERVICE_UNAVAILABLE)
	return Response(status_code=status.HTTP_204_NO_CONTENT)


@app.get("/v1/state", dependencies=protected)
async def get_state() -> dict[str, dict[str, str]]:
	sites, domains = await asyncio.gather(maps.get("sites"), maps.get("domains"))
	return {"sites": sites, "domains": domains}


@app.put("/v1/sites", dependencies=protected)
async def replace_sites(values: dict[str, str]) -> dict[str, object]:
	return await maps.replace("sites", values)


@app.put("/v1/domains", dependencies=protected)
async def replace_domains(values: dict[str, str]) -> dict[str, object]:
	return await maps.replace("domains", values)


@app.patch("/v1/{kind}/{key}", dependencies=protected)
async def patch_mapping(kind: str, key: str, value: AddressUpdate) -> dict[str, object]:
	return await maps.update(kind, key, value.address)


@app.delete("/v1/{kind}/{key}", dependencies=protected)
async def delete_mapping(kind: str, key: str) -> Response:
	await maps.delete(kind, key)
	return Response(status_code=status.HTTP_204_NO_CONTENT)


if __name__ == "__main__":
	run(app, _config.port)
