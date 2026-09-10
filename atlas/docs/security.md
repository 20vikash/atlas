# Security model

This guide records what protects an Atlas region today, and which decisions are accepted for now. Read it before you change authentication, permissions, or the tenant boundary.

## Trust boundaries

Atlas trusts the central control plane, the Metal hosts it provisions, and its own object storage. It does not trust an API caller, a guest virtual machine, or a proxy viewer.

The Atlas API at `/api/atlas` is the only tenant-facing surface. Metal Servers, provider credentials, and bare metal operations stay with a System Manager.

## Who can call the API

A service caller needs a valid Central or regional Atlas token. Send the token in the `Authorization: Bearer <token>` header. A System Manager can also use the Atlas API.

The authentication validator closes the standard Frappe guest surface. A guest can sign in and read the API reference or public key set. The realtime console stays open because its handshake and permission calls need the web process. An Atlas Admin session without verified token claims cannot use an Atlas API route.

Atlas requires the `iss`, `sub`, `aud`, `scope`, `tenant`, `iat`, and `exp` claims. It checks `nbf` when the claim is present. The audience is `atlas-admin:<region ID>`. Central uses `sub=central`, `scope=*`, and `tenant=*`. Atlas applies its regional subject and tenant policy to each regional token.

Atlas binds each key namespace to its issuer. A `central:*` key can validate only `iss=central`. The regional key can validate only `iss=atlas:<region ID>`. A regional token cannot claim Central authority.

Atlas gets Central public keys every 5 minutes. It keeps the last valid key set after a fetch failure. Atlas publishes these keys and its regional public key at `/api/atlas/jwks.json`. An unknown key ID does not cause a fetch.

## Tenant isolation

Each tenant record carries a `tenant_id`. The API reads the tenant from `X-Tenant-ID`. The value must match the token tenant unless the claim is `tenant=*`. A route returns `404` for a record of another tenant. Shared permission overrides apply the same rule to each tenant DocType.

A System image is the one shared record. Every tenant can read, boot, and download it. Only its owner can write or delete a Machine image.

Tenant `0` is reserved. A privileged virtual machine reaches every tenant through the mesh, and an address without a tenant is in the shared pool, so both need a System Manager.

## Accepted risks

**A service token is a bearer token.** Atlas does not track replay. A stolen token works until it expires, so each issuer must use a short lifetime.

**There are no quotas.** A caller can create virtual machines, reserve provider IP addresses, and request any vCPU, memory, and disk size without a limit. Cost and capacity control belong to the central control plane, not to Atlas.

**The API reference page loads a script from a CDN.** `/api/atlas/docs` is unauthenticated and serves Scalar from `cdn.jsdelivr.net` without a pinned version or an integrity hash. The page shares an origin with the site, so a compromised CDN would run in the browser of a signed-in viewer. Pin the version and add an integrity hash, or serve the bundle from the app.

**A signed image download URL lives for 24 hours.** The URL is a bearer capability for that artifact. Atlas sets `Cache-Control: no-store`, but a caller can pass the URL on.

**A console token is a bearer capability.** It is a 48-character value that expires after 60 seconds and is single use. The realtime handler accepts it from a guest, because the token is the only credential the console needs.

## Rules for a change

Keep an API route thin. Put the permission check on the domain object, so a whitelisted method is safe on its own. A route is a second line of defence, not the only one.

Do not return a Frappe message to a caller unless the message is written for a caller. Raise `AtlasUserError` for a message that a caller may read. Every other failure returns a generic message for its status, and Atlas logs the traceback.

Run a queued job as Administrator with `run_as_admin`. A job must not depend on the permissions of the user that enqueued it, and it must not run inside a request.
