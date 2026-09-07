# Control daemon

The control daemon serves the site and custom-domain maps. Every other setting comes from `/etc/atlas/proxy-control.toml`, which Atlas writes over SSH. See [Setup](setup.md#configuration-file).

The daemon listens on `127.0.0.1:9000` only. Reach it through the proxy at `https://<control.domain>/`, which OpenResty routes to the loopback port. Every request therefore carries the regional wildcard certificate.

The daemon reads the OpenResty maps through a Unix socket. The daemon does not store a second copy of the maps.

The control domain is reserved in the site map. `PATCH` and `DELETE` on its subdomain return `409`, and a full `PUT` skips it, so a mapping can never take the name. The route is decided before the map is read, so an entry could not take effect even if one existed.

## Authentication

The daemon accepts 2 bearer token types:

- A password checked against a bcrypt hash.
- A JWT checked against a JWKS endpoint.

Both come from the `[auth]` section of the configuration file, which the daemon reads on each request. A credential change therefore applies without a restart.

### Password

Send the raw password in a bearer token. The daemon checks it against `auth.password_hash`.

```sh
export ATLAS_PROXY_CONTROL_PASSWORD='replace-with-the-raw-password'
export ATLAS_PROXY_CONTROL_URL='https://proxy-001.iad.frappe.dev'
curl \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  "$ATLAS_PROXY_CONTROL_URL/v1/state"
```

If JWKS is not configured, password authentication is mandatory:

- The daemon returns `401` when no password hash is available.
- The daemon returns `401` when the password does not match the hash.

If JWKS is configured, a valid JWKS token can authorize the request without a password.

### JWKS

Set these values in the `[auth]` section to accept a JWT from an external issuer:

- `jwks_url`
- `jwks_audience_id`

The daemon fetches the keys, matches `kid`, and checks the signature, `exp`, and `aud` claims.

JWKS accepts only these algorithms:

- `RS256`, `RS384`, and `RS512`
- `ES256`, `ES384`, and `ES512`
- `PS256`, `PS384`, and `PS512`
- `EdDSA`

```sh
curl \
  -H "Authorization: Bearer $JWT" \
  "$ATLAS_PROXY_CONTROL_URL/v1/state"
```

A request is authorized when it matches the password or a valid JWKS token.

The bearer password can be replayed by anyone who observes it, so send it only over the HTTPS control domain.

## Health endpoints

Use `/healthz` to check the daemon without authentication. Use `/readyz` with authentication to check the OpenResty admin API.

| Method and path | Auth | Use                                      | Success result |
| --------------- | ---- | ---------------------------------------- | -------------- |
| `GET /healthz`  | No   | Check that the daemon runs.              | `200`          |
| `GET /readyz`   | Yes  | Check that the OpenResty admin API runs. | `204`          |

```sh
curl \
  "$ATLAS_PROXY_CONTROL_URL/healthz"

curl \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -o /dev/null \
  "$ATLAS_PROXY_CONTROL_URL/readyz"
```

## State endpoint

Use `GET /v1/state` to get the current maps from OpenResty.

```sh
curl \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  "$ATLAS_PROXY_CONTROL_URL/v1/state"
```

The response has a `sites` object and a `domains` object.

```json
{
  "sites": {"erp": "2001:db8::10"},
  "domains": {"www.example.com": "2001:db8::20"}
}
```

## Site map

A site is a name below the proxy wildcard domain. For example, `erp` routes `erp.iad.frappe.dev` when the wildcard is `*.iad.frappe.dev`.

Use `PUT /v1/sites` to replace the full map. Send the full map when the controller starts or when it restores state.

```sh
curl \
  -X PUT \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{
    "erp": "2001:db8::10",
    "shop": "2001:db8::11"
  }' \
  "$ATLAS_PROXY_CONTROL_URL/v1/sites"
```

Use `PATCH /v1/sites/<name>` to add or change one site.

```sh
curl \
  -X PATCH \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{
    "address": "2001:db8::10"
  }' \
  "$ATLAS_PROXY_CONTROL_URL/v1/sites/erp"
```

Use `DELETE /v1/sites/<name>` to remove one site.

```sh
curl \
  -X DELETE \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  "$ATLAS_PROXY_CONTROL_URL/v1/sites/erp"
```

Use `"-"` as a site address to stop traffic with a `503` response. Do not use an empty address.

## Custom-domain map

A custom domain is a customer domain such as `www.example.com`. The proxy sends its TLS traffic to the site VM without TLS termination.

Use `PUT /v1/domains` to replace the full map.

```sh
curl \
  -X PUT \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{
    "www.example.com": "2001:db8::20"
  }' \
  "$ATLAS_PROXY_CONTROL_URL/v1/domains"
```

Use `PATCH /v1/domains/<name>` to add or change one custom domain.

```sh
curl \
  -X PATCH \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  -H 'Content-Type: application/json' \
  -d '{
    "address": "2001:db8::20"
  }' \
  "$ATLAS_PROXY_CONTROL_URL/v1/domains/www.example.com"
```

Use `DELETE /v1/domains/<name>` to remove one custom domain.

```sh
curl \
  -X DELETE \
  -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" \
  "$ATLAS_PROXY_CONTROL_URL/v1/domains/www.example.com"
```

The map can use a wildcard suffix key. The key `*-shop.example.com` matches `a-shop.example.com` and `a-b-shop.example.com`.

An exact domain key has priority. The most specific wildcard suffix has the next priority.

## Wildcard certificate

Configure the regional wildcard certificate in the `[tls]` section. Run `proxy-control` to validate and activate the certificate. See [Setup](setup.md#change-the-configuration).

The apply step checks the certificate date, name, and private key. It writes the certificate files below `/var/lib/nginx/certs/<region>`, writes the region to `/var/lib/nginx/region`, checks the OpenResty configuration, and reloads OpenResty. It restores the previous files when any step fails.

Do not use this for a custom-domain certificate. Install a custom-domain certificate on its site VM.

## Error responses

| Status | Meaning                                                           |
| ------ | ----------------------------------------------------------------- |
| `400`  | The request data is not valid.                                    |
| `401`  | The password is not valid, and no valid JWKS token was presented. |
| `409`  | The key is reserved for the control daemon.                       |
| `502`  | The daemon cannot use the OpenResty admin API.                    |
| `503`  | OpenResty is not ready.                                           |
