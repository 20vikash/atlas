# Control daemon

The control daemon manages the site and custom-domain maps for one regional proxy. Atlas writes all other settings to `/etc/atlas/proxy-control.toml`.

The daemon listens only on `127.0.0.1:9000`. OpenResty exposes it at `https://<control.domain>/` with the regional wildcard certificate. `atlas-proxy-control.socket` keeps the port open while the daemon restarts. Without systemd socket activation, the daemon binds the port itself.

The control domain is reserved. A site map cannot use its subdomain.

## Authentication

Every route except `GET /healthz`, `GET /docs`, and `GET /docs/swagger.json` needs a bearer token. Use the raw control password or a JWT from the configured JWKS issuer.

```sh
export ATLAS_PROXY_CONTROL_URL='https://proxy-001.iad.frappe.dev'
export ATLAS_PROXY_CONTROL_PASSWORD='replace-with-the-raw-password'
curl -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" "$ATLAS_PROXY_CONTROL_URL/v1/state"
```

The daemon reads the `[auth]` configuration for each request. A password or JWKS change needs no daemon restart.

## API reference

Use `GET /docs` for the Scalar API reference and `GET /docs/swagger.json` for its OpenAPI schema. Select `BearerAuth` in the reference to send a password or JWT. Scalar removes the credential when the page reloads.

The reference gives request fields, response fields, and examples. It selects cURL by default and lists all other client libraries in More.

## Health

| Route | Auth | Use | Result |
| --- | --- | --- | --- |
| `GET /healthz` | No | Check that the daemon runs. | `200` |
| `GET /readyz` | Yes | Check that OpenResty accepts control requests. | `204` |

## Routing maps

Use `GET /v1/state` to read both maps during reconciliation.

| Route | Use |
| --- | --- |
| `PUT /v1/sites` | Replace every site route after a controller restart or full reconciliation. |
| `PATCH /v1/sites/<name>` | Add or change one site route. |
| `DELETE /v1/sites/<name>` | Remove one site route. It succeeds when the site is absent. |
| `PUT /v1/domains` | Replace every custom-domain route after a controller restart or full reconciliation. |
| `PATCH /v1/domains/<domain>` | Add or change one custom-domain route. |
| `DELETE /v1/domains/<domain>` | Remove one custom-domain route. It succeeds when the domain is absent. |

All routing routes need `BearerAuth`. `PUT` replaces the complete map, so entries absent from the request are removed. `PATCH` changes one route and keeps all other routes.

### Site routes

A site name is one label below the wildcard domain. For example, `erp` routes `erp.iad.frappe.dev` when the wildcard is `*.iad.frappe.dev`.

```sh
curl -X PUT \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{"erp":"2001:db8::10","shop":"2001:db8::11"}' \
  "$ATLAS_PROXY_CONTROL_URL/v1/sites"

curl -X PATCH \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{"address":"2001:db8::10"}' \
  "$ATLAS_PROXY_CONTROL_URL/v1/sites/erp"
```

Use `"-"` as a site address to return `503` for that site. Do not use an empty address.

### Custom-domain routes

A custom-domain key is a complete customer domain, such as `www.example.com`. The proxy sends its TLS traffic to the site VM without TLS termination.

```sh
curl -X PUT \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{"www.example.com":"2001:db8::20"}' \
  "$ATLAS_PROXY_CONTROL_URL/v1/domains"
```

A custom-domain map can use a wildcard suffix key. `*-shop.example.com` matches `a-shop.example.com` and `a-b-shop.example.com`. Exact keys have priority, followed by the most specific wildcard suffix.

## Errors

| Status | Meaning |
| --- | --- |
| `401` | The bearer token is missing or invalid. |
| `409` | The site name is reserved for the control daemon. |
| `422` | The request does not match the API schema. |
| `502` | OpenResty cannot apply or return a map. |
| `503` | OpenResty is not ready. |
