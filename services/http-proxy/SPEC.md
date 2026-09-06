# HTTP Proxy Component Specification

[Root specification](../../SPEC.md)

## Purpose

The HTTP proxy runs on one regional virtual machine. It routes site traffic to site VMs over IPv6.

Atlas installs it and owns its configuration. One file, `/etc/atlas/proxy-control.toml`, carries the credentials and the wildcard certificate. Atlas writes that file over SSH and runs `nginx/setup.sh` or `proxy-control` to apply it. The HTTP API serves the site and custom-domain maps only.

## Layout

```text
control/                     Python control daemon
  proxy_control/             Control package
nginx/                       OpenResty and Nginx configuration
  lua/                       Routing logic
  pages/                     Error pages
  systemd/                   systemd units
tests/                       Python tests
docs/                        Setup and operation docs
```

## Software

The control daemon uses Python 3.14. The data plane uses OpenResty, Nginx, Lua, and systemd.

## Interfaces

The control daemon uses TCP port `9000` by default. OpenResty uses a private Unix socket. Do not expose the socket to a network.

`nginx/setup.sh` is the only installer. It runs on a plain Ubuntu 24.04 virtual machine and is safe to repeat.

## Validation

From the service root, run `python -m pip install --editable 'control[test]'` and `python -m pytest -q control/tests`.

## Documentation

Read [`README.md`](README.md) first. Then read the relevant file in [`docs/`](docs/).

## Scope

The proxy handles regional site traffic. It does not manage VM lifecycle or private VM networking.

## Ownership

Keep control code in `control/`. Keep data-plane code in `nginx/`. Keep tests in `tests/`.
