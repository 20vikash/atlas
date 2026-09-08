# OpenResty data plane

OpenResty accepts public HTTP and HTTPS traffic on each proxy node. It keeps the live route maps in shared memory. The control daemon is the only supported map writer.

## Traffic paths

| Traffic | Route path | TLS certificate |
| --- | --- | --- |
| Site HTTP on port `80` | Send HTTP to the site VM on port `80`. | Not used. |
| Auto proxy HTTP or HTTPS | Compute the VM address from the host and send HTTP to port `80`. | Regional wildcard certificate for HTTPS. |
| Site HTTPS on port `443` | Terminate TLS and send HTTP to the site VM on port `80`. | Regional wildcard certificate. |
| Custom-domain HTTP on port `80` | Send HTTP to the site VM on port `80`. | Not used. |
| Custom-domain HTTPS on port `443` | Read SNI and send TLS to the site VM on port `443`. | Certificate on the site VM. |
| Regional or node control HTTPS | Terminate TLS and send HTTP to `127.0.0.1:9000`. | Regional wildcard certificate. |

SNI means Server Name Indication. OpenResty drops an HTTPS connection with no SNI. It uses the placeholder certificate and an error page for an unknown custom domain.

## Control names

The apply command writes the first labels from `control.domain` and `control.node_domain` to `/var/lib/nginx/control-subdomain`. OpenResty routes these labels to the daemon before it reads the site map. In a normal Atlas configuration, these labels are `proxy` and the local `proxy-NNN` name.

The control daemon rejects `proxy` and every `proxy-*` site key. This rule keeps regional and node control traffic separate from tenant site routes.

## Site traffic

`/var/lib/nginx/region` stores the wildcard zone without `*.`. A host exactly one label below this zone uses the site map. For example, `erp.par-1.example.com` reads the `erp` key.

The wildcard certificate covers site HTTPS traffic. An address of `-` returns `503` for that site.

## Auto proxy traffic

A configured host form such as `site-<tenant>-<vm>` or `*-vm-<tenant>-<vm>` below the wildcard zone routes directly to its VM address on port `80`. The tenant and VM fields are lowercase hexadecimal values with a maximum of 8 and 16 digits. For example, `site-7-2a` with address prefix `fdaa:1` routes to `[fdaa:1:0:7:0:0:0:2a]:80`.

`/var/lib/nginx/auto-proxy` holds the address prefix and the configured host prefixes. An empty file disables static routing.

## Custom-domain traffic

OpenResty does not decrypt custom-domain TLS traffic. It connects to the site VM on port `443` and sends a PROXY protocol v2 header before the TLS data. The site VM must accept PROXY protocol v2 and hold the certificate for the requested domain.

The HTTP and stream workers use separate maps. A custom-domain mutation passes through the private SNI bridge so both workers receive the same state.

## Route state

OpenResty writes its live maps below `/var/lib/nginx` after a short delay:

| File | Data |
| --- | --- |
| `map.json` | Site map. |
| `domains-http-map.json` | Custom-domain HTTP map. |
| `sni-map.json` | Custom-domain TLS map. |
| `auto-proxy` | Address and host prefixes for static routes. The control daemon writes it. |

The control daemon also stores the complete cluster snapshot in `/var/lib/nginx/cluster-state.json`. This snapshot contains both maps and their generation. At daemon startup, the control daemon installs its durable snapshot into OpenResty. It then replaces it with a newer peer snapshot when one is available.

Do not edit these files while the services run.

## Certificates

The setup script creates `/var/lib/nginx/certs/_placeholder`. This certificate lets OpenResty start before Atlas sends the regional certificate.

The apply command validates and installs the active wildcard certificate below `/var/lib/nginx/certs/<region>`. It then updates `/var/lib/nginx/certs/fullchain.pem` and `/var/lib/nginx/certs/privkey.pem`.

## Private interfaces

| Path | Use |
| --- | --- |
| `/run/nginx/admin.sock` | Map API for the local control daemon. |
| `/run/nginx/sni-bridge.sock` | Map bridge between HTTP and stream workers. The owner is `root:nginx`. |
| `/var/lib/nginx/acme` | ACME challenge files for the wildcard certificate. |

Do not expose either socket outside the VM.

## Checks

```sh
sudo systemctl status openresty.service
sudo /usr/local/openresty/nginx/sbin/nginx -t -c /etc/nginx/nginx.conf
journalctl -u openresty.service -f
```
