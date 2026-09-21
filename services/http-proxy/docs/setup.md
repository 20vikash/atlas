# Install the HTTP proxy

Atlas normally installs and configures each proxy. Use this guide to install a node by hand or to check the installed services.

## Requirements

Use Ubuntu 24.04 and `root` or `sudo`. Give the VM access to the Ubuntu, OpenResty, and deadsnakes package servers. The control daemon needs Python 3.14.

Each node needs a public IPv4 address, a stable `proxy-NNN.<wildcard-domain>` A record, and HTTPS access to every configured peer. The regional wildcard certificate must cover the node address and `proxy.<wildcard-domain>`.

## Install

1. Copy this component to the VM.
2. Write `/etc/atlas/proxy-control.toml` with mode `0600` and owner `root`.
3. Run `sudo ./nginx/setup.sh`.
4. Check `openresty.service`, `atlas-proxy-control.socket`, and `atlas-proxy-control.service`.

The setup script creates the locked `frappe` account, installs OpenResty and the daemon, and creates a placeholder certificate. A repeated run keeps the installed package and restarts OpenResty.

Before the daemon can become ready, write the complete configuration described in [proxy configuration](configuration.md).

## Checks

```sh
sudo systemctl status openresty.service atlas-proxy-control.socket atlas-proxy-control.service
curl -fsS -o /dev/null http://127.0.0.1:9000/healthz
curl -fsS -o /dev/null http://127.0.0.1:9000/readyz
journalctl -u atlas-proxy-control.service -f
```

`/healthz` checks OpenResty and its routes. `/readyz` also checks cluster synchronization and the leader.
