# API clients

`clients/` holds two generated Python clients and the OpenAPI documents that they come from. [openapi-python-client](https://github.com/openapi-generators/openapi-python-client) generates them. The clients are in Git, so a user can install one directly.

| Directory | Package | API |
| --- | --- | --- |
| `clients/atlas-client` | `atlas_client` | Atlas tenant API |
| `clients/atlas-proxy-client` | `atlas_proxy_client` | HTTP proxy control API |

## Regenerate

`scripts/generate-api-client.py` writes the OpenAPI document and then the client. It imports the application and reads the specification in memory. A site, a database, and a running server are not necessary.

```bash
pip install openapi-python-client==0.29.1
./scripts/generate-api-clients.sh
```

`scripts/generate-api-clients.sh` writes both clients. It runs the `atlas-client` client with the bench environment python, which it finds two directories above the repository, and it runs the `atlas-proxy-client` client with `uv`, which supplies the `atlas-proxy-control` package. Set `ATLAS_PYTHON` when the bench environment is in another location. To write one client, call the Python script directly.

```bash
python scripts/generate-api-client.py atlas-client
python scripts/generate-api-client.py atlas-proxy-client
```

Commit the result. The `atlas-client` command needs `frappe` and the `atlas` app on the Python path, so run it from a bench environment. The `atlas-proxy-client` command needs the `atlas-proxy-control` package. It writes a temporary configuration file, because `proxy_control.main` loads its configuration at import time.

The generator uses no post hooks. It does not lint or format its output, so the committed client is the same for every environment.

The client method names come from the OpenAPI operation IDs. Atlas uses the route function name. The HTTP proxy control daemon uses `generate_unique_id_function` to do the same.

## CI

The `CI` workflow regenerates the `atlas-client` client, and the `Atlas HTTP proxy tests` workflow regenerates the `atlas-proxy-client` client. Each workflow fails when the result is different from the committed client. Regenerate the client in the same commit as an API change.
