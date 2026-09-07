# Setup

Use this guide to install the proxy on a virtual machine. Atlas does this for a Proxy Server record. Follow the manual steps when you set up a proxy by hand.

## Before you start

Use Ubuntu 24.04 and `root` or `sudo`. Give the VM access to the Ubuntu, OpenResty, and deadsnakes package servers.

The daemon needs Python 3.14. The setup script adds the deadsnakes PPA and creates its virtual environment with `python3.14`.

The setup script creates the locked `frappe` account with passwordless `sudo`. It also creates the configuration file and a placeholder certificate when they do not exist.

## Install

1. Copy this component to the VM.
2. Write `/etc/atlas/proxy-control.toml`. See [Configuration file](#configuration-file).
3. Run the setup script:

  ```sh
  sudo ./nginx/setup.sh
  ```

The script installs OpenResty and the control daemon, enables their units, and applies the certificate the configuration file carries. It is safe to run again. A repeated run keeps the installed OpenResty package and reloads the running service, so the open connections stay up.

An empty configuration file stops the script before it applies anything and before it starts the daemon. Write the file and run the script again.

4. Check that both services are enabled:

  ```sh
  sudo systemctl is-enabled openresty.service atlas-proxy-control.service
  ```

## Configuration file

`/etc/atlas/proxy-control.toml` is the only configuration source. It holds the credentials and the regional wildcard certificate, so its mode is `0600` and its owner is `root`.

```toml
[control]
domain = "proxy-001.par-1.example.com"
admin_socket = "/run/nginx/admin.sock"
cert_dir = "/var/lib/nginx/certs"

[auth]
password_hash = "$2b$12$replace-with-a-bcrypt-hash"
jwks_url = "https://issuer.example.com/jwks.json"
jwks_audience_id = "atlas-proxy-control"

[tls]
wildcard_domain = "*.par-1.example.com"
fullchain_pem = '''
-----BEGIN CERTIFICATE-----
...
-----END CERTIFICATE-----
'''
private_key_pem = '''
-----BEGIN PRIVATE KEY-----
...
-----END PRIVATE KEY-----
'''
```

`[control]` and `[auth]` are optional. Without credentials the daemon serves `/healthz` and refuses every protected request.

`[tls]` is required. A proxy without a valid wildcard certificate refuses to configure. The daemon and `proxy-control` name missing values.

`wildcard_domain` also sets the region. The apply step writes it to `/var/lib/nginx/region`, which is what separates a site subdomain from a custom domain.

`control.domain` is the name that reaches this daemon. It must sit one label below the wildcard name, so the certificate covers it and its first label is the reserved subdomain. The apply step writes that label to `/var/lib/nginx/control-subdomain`.

The daemon binds `127.0.0.1` only. OpenResty routes the control domain to it on port 443, so every request carries the wildcard certificate and no port has to be open to a network. A site map may not use the reserved subdomain: `PATCH` and `DELETE` on it return `409`, and a full `PUT` installs every other entry and skips it.

Use a PEM literal string with `'''`. PEM text contains no `'''`, so no value needs escaping.

## Change the configuration

Write the new file, then apply it:

```sh
sudo /opt/atlas/proxy-control/bin/proxy-control
```

The command installs the certificate, writes the region file, and reloads OpenResty. It exits non-zero when the certificate is missing, when the certificate and the key do not match, or when the certificate does not cover the wildcard domain.

The daemon reads its credentials on each request, so a credential change needs no restart. It always listens on `127.0.0.1:9000`.

## Configure the maps

The HTTP API carries the maps only:

1. Send `PUT /v1/sites` with the full site map.
2. Send `PUT /v1/domains` with the full custom-domain map.

See [Control daemon](control-daemon.md) for request examples.

Do not direct public DNS traffic to the VM before the certificate and the maps are in place.

## Check a running proxy

```sh
sudo systemctl status openresty.service atlas-proxy-control.service
curl -fsS -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" http://127.0.0.1:9000/healthz
curl -fsS -H "Authorization: Bearer $ATLAS_PROXY_CONTROL_PASSWORD" -o /dev/null http://127.0.0.1:9000/readyz
```

## Service commands

```sh
sudo systemctl reload openresty.service
sudo systemctl restart atlas-proxy-control.service
journalctl -u openresty.service -f
journalctl -u atlas-proxy-control.service -f
```

A reload keeps the open connections. Use `systemctl restart openresty.service` only when a reload cannot apply the change.

`atlas-proxy-control.socket` owns `127.0.0.1:9000`. It keeps the port open while the daemon restarts, so a request waits instead of getting a `502`. Restart the socket unit only to change the listening address.
