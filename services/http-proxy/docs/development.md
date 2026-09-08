# Development

Use this guide to change the proxy or its tests.

## Read these files first

Read these files before you change code:

1. `README.md`
2. `docs/setup.md`
3. `docs/control-daemon.md`
4. `docs/openresty.md`
5. `nginx/nginx.conf`
6. `control/proxy_control/main.py`
7. The Lua file for the route that you change.
8. The related test file in `tests`.

## Folder tree

```text
control/
  proxy_control/
    main.py                       FastAPI map API and startup wiring.
    config.py                     Read /etc/atlas/proxy-control.toml.
    apply.py                      The proxy-control command. Install the certificate.
    auth.py                       JWKS bearer token authentication.
    certificates.py               Validate and install wildcard certificates.
    client.py                     Send HTTP requests through the Unix socket.
    cluster.py                    Elect the leader and replicate route generations.
    mappings.py                   Read and change route maps.
    server.py                     Start the IPv4 and IPv6 daemon listeners.
  tests/                          Unit tests for the control daemon.
  pyproject.toml                  Python package list and the proxy-control command.

nginx/
  setup.sh                        Install the proxy on Ubuntu.
  nginx.conf                      Main OpenResty configuration.
  lua/http/                       HTTP routes, maps, and map storage.
  lua/stream/                     TLS SNI routes and the SNI bridge.
  pages/                          HTML error pages.
  systemd/                        OpenResty and daemon systemd units.

tests/
  test_proxy.py                   Site route tests.
  test_custom_domain_proxy.py     Custom-domain tests.
  test_build.py                   Image and install tests.
  test_latency.py                 Route and map size tests.
```

## Change guide

Change the matching file for API, cluster, configuration, authentication, map, or certificate changes:

- `main.py`, `cluster.py`, `config.py`, `apply.py`, `auth.py`, `mappings.py`, or `certificates.py`

Change `config.py` and `docs/setup.md` together when you add a configuration key. Atlas renders the same file in `atlas/service/core/proxy/configuration.py`, so change that too.

Change `nginx/lua/http/admin.lua` when you change map storage or the private OpenResty API.

Change `nginx/lua/http/router.lua` or `nginx/lua/http/plain_router.lua` for HTTP routing.

Change `nginx/lua/stream` for custom-domain TLS routing. Change both HTTP and stream code for custom-domain map changes.

Change `nginx/setup.sh` for installed files, packages, users, paths, or systemd units. Keep the install tests in sync when setup files move.

Change `PYTHON_VERSION` in `nginx/setup.sh`, `requires-python` in `control/pyproject.toml`, `target-version` in `ruff.toml`, and the CI Python version together.

## Run the proxy locally

Create a virtual environment and install the control package with its test dependencies:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install --editable 'control[test]'
```

Start the control daemon with its default configuration:

```sh
python -m proxy_control.main
```

The daemon listens on port `9000`. Install OpenResty with [Setup](setup.md) before you use the readiness or map endpoints.

## Run the tests

Run the control daemon tests from the service root:

```sh
python -m pip install --editable 'control[test]'
python -m pytest -q control/tests
```

Run a focused test for one area:

```sh
python -m pytest -q control/tests/test_config.py
python -m pytest -q control/tests/test_auth.py
python -m pytest -q control/tests/test_apply.py
python -m pytest -q control/tests/test_cluster.py
```

## Check a change

```sh
bash -n nginx/setup.sh
python3 -m compileall -q control/proxy_control control/tests
git diff --check
```

## Files that the proxy writes

OpenResty writes route maps below `/var/lib/nginx`. The control daemon writes `cluster-state.json` in the same directory. Do not edit these files while the services run.

The `proxy-control` command writes the region file and the wildcard certificate files. Change `/etc/atlas/proxy-control.toml` and run it again, rather than editing those files.
