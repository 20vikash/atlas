# Atlas HTTP proxy

Atlas HTTP proxy runs on one virtual machine for one region. It routes site and custom-domain traffic to site VMs over IPv6.

Atlas installs it and pushes `/etc/atlas/proxy-control.toml` over SSH. The control daemon reads that file and serves the site and custom-domain maps on IPv4 and IPv6 port `9000`.

Read these documents in this order:

1. [Setup](docs/setup.md)
2. [Control daemon](docs/control-daemon.md)
3. [OpenResty](docs/openresty.md)
4. [Development](docs/development.md)

The control daemon is the public API and it serves the maps only. Every other setting comes from the configuration file. OpenResty uses a private Unix socket. Do not expose it to a network.

## Main paths

```text
nginx/setup.sh                  Install the proxy on an Ubuntu VM.
nginx/nginx.conf                Configure OpenResty.
nginx/lua/http/                 Route HTTP traffic and store HTTP maps.
nginx/lua/stream/               Route TLS traffic by SNI.
nginx/pages/                    Store the HTML error pages.
nginx/systemd/                  Store the systemd units.
control/proxy_control/          Store the control daemon package.
control/pyproject.toml          Define Python packages for the daemon.
docs/                           Store operator and developer guides.
tests/                          Store the Python test suites.
```

## Quick start

```sh
sudo ./nginx/setup.sh
```

The script installs OpenResty and the control daemon, enables both units, and applies the certificate the configuration file carries. Run it again after you change the configuration file, or run `/opt/atlas/proxy-control/bin/proxy-control` for the certificate alone.

A fresh install has an empty configuration file. The proxy starts with a placeholder certificate. The control daemon serves `/healthz` and refuses protected requests until Atlas sends a credential.

Follow the [setup guide](docs/setup.md) to configure the proxy.

## Local development

Install the control package with its test dependencies, then run the control daemon tests:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install --editable 'control[test]'
python -m pytest -q control/tests
```

Install the full proxy on Ubuntu 24.04 with the [setup guide](docs/setup.md). Use the [development guide](docs/development.md) to change the package or run the daemon locally.

## License

Atlas WG Mesh is licensed under [AGPL-3.0](../../license.txt).
