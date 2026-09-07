# HTTP Proxy Component Specification

[Root specification](../../SPEC.md)

## Purpose

The HTTP proxy runs on one regional VM and routes site traffic to site VMs over IPv6. Atlas owns its configuration and writes `/etc/atlas/proxy-control.toml` over SSH.

## Layout

```text
control/       Python control daemon
nginx/         OpenResty, Lua, and systemd units
tests/         Component tests
docs/          Setup and operation guides
```

## Interfaces

- The control daemon serves `127.0.0.1:9000`. `atlas-proxy-control.socket` holds the port during daemon restarts.
- OpenResty routes `control.domain` to the daemon before it reads the site map. That subdomain is reserved.
- The public API is available through HTTPS on port `443`.
- OpenResty and the daemon use a private Unix socket.

## Ownership

Keep control code in `control/` and data-plane code in `nginx/`. The proxy manages regional traffic only. It does not manage VM lifecycle or private VM networking.

## Validation

From this directory, run `python -m pip install --editable 'control[test]'` and `python -m pytest -q control/tests`.

## Documentation

Read [`README.md`](README.md), then the relevant guide in [`docs/`](docs/).
