# Atlas HTTP proxy

Atlas HTTP proxy is a regional cluster of one to five proxy virtual machines. Every ready proxy serves site and custom-domain traffic. The cluster replicates ordered route-map generations between nodes.

Clients and Atlas use `proxy.<wildcard-domain>` as the regional control address. Route 53 normally returns ready cluster members. It returns every member when all health checks fail. Each member also has a stable `proxy-NNN.<wildcard-domain>` address for peer communication and operations.

Atlas installs the component and writes `/etc/atlas/proxy-control.toml` over SSH. The file contains the regional certificate, external authentication settings, cluster credentials, and the complete peer list. A joining node gets route state from the peer with the highest generation. Atlas does not copy route maps to a joining node.

Read these documents in this order:

1. [Setup and configuration](docs/setup.md)
2. [High-availability design](docs/high-availability.md)
3. [Control daemon API](docs/control-daemon.md)
4. [OpenResty data plane](docs/openresty.md)
5. [Development](docs/development.md)

## Request path

```text
Atlas or API client
        |
        v
proxy.<wildcard-domain> through health-checked DNS
        |
        v
any ready proxy -> elected leader -> acknowledgement threshold -> response
```

The selected proxy handles reads locally. It forwards a mutation to the elected leader when required. The leader applies the mutation, assigns its generation, sends it to peers concurrently, and responds after the configured acknowledgement threshold is met.

OpenResty handles public traffic and exposes the control daemon through the regional wildcard certificate. The daemon listens on `127.0.0.1:9000`. OpenResty and the daemon exchange map data through private Unix sockets.

## Main paths

```text
nginx/setup.sh                  Install the proxy on an Ubuntu VM.
nginx/nginx.conf                Configure OpenResty.
nginx/lua/http/                 Route HTTP traffic and store HTTP maps.
nginx/lua/stream/               Route TLS traffic by SNI.
nginx/systemd/                  Store the systemd units.
control/proxy_control/          Store the control daemon and cluster code.
control/tests/                  Store control daemon unit tests.
docs/                           Store operator and developer guides.
tests/                          Store data-plane tests.
```

## Quick start

```sh
sudo ./nginx/setup.sh
```

The script installs OpenResty and the control daemon. A fresh install has an empty configuration file and a placeholder certificate. The daemon becomes ready after Atlas sends a complete configuration and the node restores its cluster state.

Follow the [setup guide](docs/setup.md) for the required configuration.

## Local development

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install --editable 'control[test]'
python -m pytest -q control/tests
```

Use the [development guide](docs/development.md) for the complete checks.

## License

Atlas HTTP proxy is licensed under [AGPL-3.0](../../license.txt).
