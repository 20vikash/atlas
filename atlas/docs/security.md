# Security model

This guide records what protects an Atlas region today, and which decisions are accepted for now. Read it before you change authentication, permissions, or the tenant boundary.

## Trust boundaries

Atlas trusts the central control plane, the Metal hosts it provisions, and its own object storage. It does not trust an API caller, a guest virtual machine, or a proxy viewer.

The Atlas API at `/api/atlas` is the only tenant-facing surface. Metal Servers, provider credentials, and bare metal operations stay with a System Manager.

## Who can call the API

A caller needs a Frappe session from a user with the `Atlas Admin` role, or a valid central token. The role does not grant Desk access. The authentication validator restricts an Atlas Admin who is not a System Manager to `/api/atlas` routes, and leaves every other user and route to Frappe.

A central token is a JWT that the issuer at `central_jwks_url` signs. Atlas accepts it when it is unexpired and carries the audience `atlas-<region ID>-admin`. An unknown key ID is refused without a key set refetch, so a caller cannot make Atlas fetch on demand. A valid token signs the request in as `central-admin@atlas.local`, which holds the `Atlas Admin` role alone.

## Tenant isolation

Each tenant record carries a `tenant_id`. The API reads the tenant from the `X-Tenant-ID` header, and a route returns `404` for a record of another tenant. The shared permission overrides apply the same rule to every list and document read of Virtual Machine, Virtual Machine Image, and Metal Server IP Address, so a Desk or REST path cannot pass the boundary either.

A System image is the one shared record. Every tenant can read, boot, and download it. Only its owner can write or delete a Machine image.

Tenant `0` is reserved. A privileged virtual machine reaches every tenant through the mesh, and an address without a tenant is in the shared pool, so both need a System Manager.

## Accepted risks

**The tenant header is not bound to the caller.** Any holder of an `Atlas Admin` session or a valid central token can set `X-Tenant-ID` to any tenant and act as that tenant. The header selects a tenant; it does not prove one. This is safe only while every credential belongs to the central control plane. Before Atlas issues a credential to anyone else, the tenant must move into the token as a claim and the header must become a check.

**A central token is a bearer token.** Atlas checks the signature, the expiry, and the audience. It does not check an issuer, a subject, or a scope, and it does not track replay. A stolen token is usable until it expires, so the issuer must keep the lifetime short.

**There are no quotas.** A caller can create virtual machines, reserve provider IP addresses, and request any vCPU, memory, and disk size without a limit. Cost and capacity control belong to the central control plane, not to Atlas.

**The API reference page loads a script from a CDN.** `/api/atlas/docs` is unauthenticated and serves Scalar from `cdn.jsdelivr.net` without a pinned version or an integrity hash. The page shares an origin with the site, so a compromised CDN would run in the browser of a signed-in viewer. Pin the version and add an integrity hash, or serve the bundle from the app.

**A signed image download URL lives for 24 hours.** The URL is a bearer capability for that artifact. Atlas sets `Cache-Control: no-store`, but a caller can pass the URL on.

**A console token is a bearer capability.** It is a 48-character value that expires after 60 seconds and is single use. The realtime handler accepts it from a guest, because the token is the only credential the console needs.

## Rules for a change

Keep an API route thin. Put the permission check on the domain object, so a whitelisted method is safe on its own. A route is a second line of defence, not the only one.

Do not return a Frappe message to a caller unless the message is written for a caller. Raise `AtlasUserError` for a message that a caller may read. Every other failure returns a generic message for its status, and Atlas logs the traceback.

Run a queued job as Administrator with `run_as_admin`. A job must not depend on the permissions of the user that enqueued it, and it must not run inside a request.
