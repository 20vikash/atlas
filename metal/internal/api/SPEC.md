# api: Metal HTTP server

[internal SPEC](../SPEC.md) · endpoint guide: [docs/api.md](../../docs/api.md)

## Purpose

Almost nothing here waits for the host. A mutation validates its request, stores desired state, wakes the reconciler, and returns. The controller learns the outcome by polling, not by holding a connection open.

That is why this package is thin: it owns request validation, the public response shape, and the mapping from domain errors to status codes. It owns no VM, image, or host state.

## Types

`New(Config, Dependencies)` validates configuration and dependencies, then returns a configured Echo router. `Server` holds the injected services and every handler.

`Dependencies` carries the VM manager, snapshot store, host service, wake function, serial broker, and trusted key store. Each is an interface declared here, so this package depends on no implementation.

## Request flow

```text
correlation -> log -> group authentication -> handler
                                                 |
                             decode strict JSON -+
                             validate            |
                             call a service      |
                             wake the reconciler |
                             respond ------------+
```

Correlation runs first, so every log line and every error carries the same IDs. Each request gets an `X-Request-ID` and an `X-Operation-ID`, returned as response headers and included in logs. A caller-supplied value is kept when it is safe to echo, and replaced when it is not.

JSON decoding is strict: unknown fields and trailing values are rejected, so a misspelled field fails instead of being silently dropped. Bodies are size capped.

## Routes

```text
GET    /health                    public
GET    /docs                      public
GET    /docs/swagger.json         public

POST   /v1/sync

PUT    /v1/vms/:id                create, idempotent on the caller's ID
GET    /v1/vms
GET    /v1/vms/:id
PUT    /v1/vms/:id/power
POST   /v1/vms/:id/restart
PUT    /v1/vms/:id/compute
PUT    /v1/vms/:id/disk
PUT    /v1/vms/:id/network
PUT    /v1/vms/:id/ssh-keys
PUT    /v1/vms/:id/metadata
DELETE /v1/vms/:id
GET    /v1/vms/:id/console        mode=tty (default) or mode=ssh

POST   /v1/vms/:id/snapshots
POST   /v1/snapshots/:id/upload
GET    /v1/snapshots/:id
DELETE /v1/snapshots/:id
```

PUT is used wherever a request replaces desired state, so a repeat is safe. POST is used only for an action that must happen again even when nothing changed, such as a restart, or for creating an addressable resource, such as a snapshot.

## Capacity

Compute and disk updates are checked against host capacity before they are stored, and an increase the host cannot satisfy is refused with `409`. Only the increase is checked, because the VM already holds what it reserves.

Nothing else is checked this way: the remaining fields do not consume a host resource.

## Status codes

| Code | Meaning |
|---|---|
| `200` | The request completed synchronously. |
| `201` | A snapshot exists. `Location` names where to fetch it. |
| `202` | Desired state is stored. Reconciliation continues. |
| `204` | Removed, with nothing to return. |

SSH key and metadata replacement try to reach a live guest first, within a short timeout, and report `200` when they succeed. A timeout is not a failure: the desired record is already stored, so `202` means the next reconcile pass applies it. The timeout belongs to [internal/vm/SPEC.md](../vm/SPEC.md).

## Authentication

Each route group names the middleware it needs, so a route cannot inherit the wrong rule.

| Middleware | Credential | Routes |
|---|---|---|
| `authenticate` | The static bearer token. Only its SHA-256 digest is configured, so the plain token never reaches this package, and the comparison is constant time. | The `/v1` controller group. |
| `requireScopes` | An Atlas-signed token that carries every scope the route names. | Routes that one Metal host calls on another. |

A static token cannot pass `requireScopes`, and an Atlas token cannot pass `authenticate`.

`requireScopes` puts the validated claims in the request context and `tokenClaims` returns them. The handler matches the claim VM identifier to the VM its own request addresses, because only the handler knows which VM that is.

The `TrustedKeyStore` dependency reports the keys. [internal/token/SPEC.md](../token/SPEC.md) owns the trust state and the verification rules.

Group authentication makes the group answer every path below it. An unknown path and a wrong method under `/v1` both return `404`.

The 3 public routes carry no VM data. Liveness must answer a probe that holds no token, and the documentation page is opened in a browser that cannot send one. Both stay open, so metald is expected to listen on a private control network.

## Errors

A domain error is mapped to one status and one safe message. An unrecognized error becomes `500` with no detail, and its cause is logged under the request ID the response carries back.

| Domain error | Status | Code |
|---|---|---|
| `vm.ErrNotFound`, `storage.ErrNotFound` | `404` | `not_found` |
| `vm.ErrConflict`, `storage.ErrInUse` | `409` | `conflict` |
| `storage.ErrImageConflict` | `409` | `image_content_conflict` |
| `storage.ErrImageIntegrity` | `422` | `image_integrity_failed` |
| `storage.ErrShuttingDown` | `503` | `unavailable` |
| `network.ErrInvalidPeers`, `storage.ErrInvalidUpload` | `400` | `invalid_request` |
| `token.ErrUnauthorized` | `401` | `unauthorized` |
| `token.ErrForbidden` | `403` | `forbidden` |

Every error body carries a `retryable` flag, so a caller does not have to know which statuses are worth another attempt. `501` is excluded: repeating it cannot change the answer.

`POST /v1/sync` addresses no resource, so it maps a not-found from host state to `503` `unavailable`. A `404` would tell the controller to stop when it must try the request again.

Responses never carry host command output, signed URL query values, or local error detail.

## Response shape

A VM response nests `desired` and `observed`. The controller compares their generations to know when a change is applied, so both are always returned together.

A response omits what the controller must not see or cannot use: transport URLs, user data, host paths, process IDs, and host user IDs.

## Related

- [docs/api.md](../../docs/api.md) gives request and response details.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines the VM contracts and the generation model.
- [internal/console/SPEC.md](../console/SPEC.md) owns the serial console the tty mode attaches to.
- [internal/token/SPEC.md](../token/SPEC.md) owns the Atlas trust state and token verification.
- [cmd/metald/SPEC.md](../../cmd/metald/SPEC.md) injects server dependencies.
