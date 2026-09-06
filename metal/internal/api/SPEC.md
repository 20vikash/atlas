# api: Metal HTTP server

[internal SPEC](../SPEC.md) · endpoint guide: [docs/api.md](../../docs/api.md)

## Purpose

Package `api` validates HTTP requests and calls small service interfaces. Mutation handlers store desired state and wake the reconciler.

## Types

`New(Config, Dependencies)` validates authentication and required services. It returns the configured Echo router or an error.

`Dependencies` contains the VM manager, snapshot store, host service, wake function, and console broker.

Request and response types are split by resource. VM files use the `vm_` prefix. `Server` owns the handlers and injected services.

Each request receives safe `X-Request-ID` and `X-Operation-ID` response headers. The server preserves valid incoming values and generates values for missing or invalid headers. JSON logs include both values.

## Request flow

```text
HTTP -> decode strict JSON -> validate -> call a service -> JSON
```

Create and lifecycle handlers return before host reconciliation completes.

The synchronization handler calls `host.Service`. The host service applies controller-owned sets and returns capacity.

## Routes

```text
GET    /health
POST   /v1/sync

PUT    /v1/vms/:id
GET    /v1/vms
GET    /v1/vms/:id
PUT    /v1/vms/:id/power
POST   /v1/vms/:id/restarts
PUT    /v1/vms/:id/compute
PUT    /v1/vms/:id/disk
PUT    /v1/vms/:id/network
PUT    /v1/vms/:id/ssh-keys
PUT    /v1/vms/:id/metadata
DELETE /v1/vms/:id

POST   /v1/vms/:id/snapshots
POST   /v1/snapshots/:id/upload
GET    /v1/snapshots/:id
DELETE /v1/snapshots/:id

GET    /v1/vms/:id/console
GET    /docs
GET    /docs/swagger.json
```

Create and asynchronous mutations return `202`. Snapshot creation returns `201`.

SSH key and metadata handlers use a 2-second immediate operation. They return `200` after success or `202` for reconciliation.

## Authentication

All `/v1` routes require a bearer token. Metal compares its SHA-256 digest with the configured digest.

## Error mapping

Error responses contain a stable `code` and a safe `message`. Domain image conflicts and integrity failures have separate codes.

The server does not return host command output or signed URL query values.

## DTO layout

Request and response objects have separate files. The VM response has nested `desired` and `observed` objects.

The VM response does not include transport URLs, user data, host paths, process IDs, or host user IDs.

## API specification

`GET /docs` serves the API page. `GET /docs/swagger.json` serves the generated OpenAPI document.

## Related

- [docs/api.md](../../docs/api.md) gives request and response details.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines the VM contracts.
- [cmd/metald/SPEC.md](../../cmd/metald/SPEC.md) injects server dependencies.
