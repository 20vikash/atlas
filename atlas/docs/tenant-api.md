# Tenant API

Atlas exposes a tenant control plane at `/api/atlas` for virtual machines, public IPv4 addresses, and virtual machine images. It does not expose Metal Servers or bare metal operations.

Use `GET /api/atlas/docs` to explore every route, request body, and response. The OpenAPI document is at `GET /api/atlas/docs/openapi.json`. This guide describes the rules that apply to every route, and how to add a route.

## Access

Each request needs Frappe authentication from a user with the `Atlas Admin` role. This role does not grant Desk access. A System Manager can also use these routes.

The authentication validator restricts an Atlas Admin who is not a System Manager to `/api/atlas` routes. Frappe handles authentication and access for every other user and route.

## Tenant

Each request must carry `X-Tenant-ID` with an unsigned 32-bit integer from 1 through 4294967295. Tenant `0` is reserved for System Manager Desk and DocType actions.

A missing or invalid tenant header returns `400`. A request for another tenant's resource returns `404`. Each resource response carries `tenant_id`.

## Conventions

An error response has `error.code`, `error.message`, and `error.fields`. Validation errors use `400`; missing authentication uses `401`; denied access uses `403`; missing resources use `404`; and invalid resource state uses `409`.

A list response carries `items`, `offset`, `limit`, and `has_more`. The default limit is 20 and the maximum limit is 100.

A `PATCH` request needs at least one supported field. A field that is not in the request does not change. A `PUT` request replaces the complete stored value.

A route that asks the host for work returns `202`. Poll the resource route for completion.

All absolute time fields use Unix timestamps in seconds. Duration fields such as `expires_in` also use seconds.

## Behavior

Use [the virtual machine specification](../vm/SPEC.md) for the virtual machine and image rules. Use [the image guide](images.md) for the image lifecycle. Use [the Metal Server specification](../metal_server/SPEC.md) for IP address reservation and release.

## Layout

`atlas/api/core/` holds the typed HTTP framework. `base.py` registers routes on the Frappe API URL map and builds responses, `binding.py` owns request decoding, `errors.py` owns the API failures, and `docs.py` owns the OpenAPI document and the Scalar reference page. `atlas/api/router.py`, `atlas/api/models.py`, and `atlas/api/routes/` hold the Atlas surface. `atlas/auth/request.py` owns API route authentication, `atlas/auth/roles.py` owns role checks, `atlas/auth/tenant.py` owns tenant parsing, and `atlas/auth/overrides.py` owns the shared tenant permission overrides that each Atlas DocType registers.

`atlas.api.router.register_atlas_api` imports every route module. The `before_request` hook calls it, so the routes exist before Frappe matches an API request.

## Add a route

A router owns one path prefix below `/api`. Add a resource group in `atlas/api/router.py`, add its routes to a matching file in `atlas/api/routes/`, and import that file in `register_atlas_api`.

```python
from pydantic import BaseModel

from atlas.api.core.base import ApiResult, StrictModel
from atlas.api.core.docs import api_docs
from atlas.api.router import machines


class MachinePayload(StrictModel):
	name: str
	cores: int = 1


class MachineResponse(BaseModel):
	id: str
	name: str
	cores: int


@machines.post("")
@api_docs(
	request_example={"name": "vm-1", "cores": 4},
	responses={201: {"description": "The machine is created."}},
)
def create_machine(payload: MachinePayload) -> ApiResult[MachineResponse]:
	"""Create a machine."""
	machine = MachineResponse(id="machine-1", name=payload.name, cores=payload.cores)
	return ApiResult(machine, status=201)
```

A route function reads request data through two reserved parameter names. Each value must be a Pydantic model or a list of one Pydantic model. The router rejects raw dictionaries, raw lists, scalar values, unions, and missing annotations when it registers a route. Atlas adds the required `X-Tenant-ID` header to each documented operation.

- `payload` decodes the JSON request body. A route on `GET` or `HEAD` cannot declare it.
- `query` decodes the query string. Pydantic converts each string value to the annotated type.
- A path parameter uses the name in the route pattern, such as `virtual_machine_id`.

Return a Pydantic model for `200`. Return `ApiResult[Pydantic model]` when the route needs another status or response header. Return `None` for `204`.

Keep the route thin. Pass validated values to the domain service. Do not pass a request dictionary to the Frappe ORM. Use the first docstring line as the OpenAPI summary and the remaining text as the description. Use `api_docs` only for examples and route-specific response codes.
