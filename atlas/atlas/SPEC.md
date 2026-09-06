# Atlas settings, providers, and host binaries

[Atlas app specification](../SPEC.md)

For the provider overview, see [docs/providers.md](../docs/providers.md).

## Purpose

Atlas talks to an infrastructure provider only through one interface. Everything provider-specific lives behind it, so adding a provider never reaches into server or virtual machine code.

This module also holds site-wide settings and builds the binaries a host downloads during installation.

## Types

| Type | Owns |
|---|---|
| `AtlasSettings` (DocType) | Provider selection, credentials, and the published binary links. |
| `ServerProvider` | The contract every server provider implements. |
| `registry` | The map from a stable provider name to its implementation. |
| `DNSProvider`, `Route53Provider` | The DNS contract and its Route 53 implementation. |
| `host_binaries` | Building and publishing `metald` and the Atlas WG Mesh CLI. |
| `ssh`, `parsing`, `mesh_address`, `object_storage` | Host access, strict input parsing, mesh addressing, and object storage. |

## Provider boundary

```text
server / vm code
      |
      v
ServerProvider  (typed create, result, catalog, power, address, and error values)
      |
      +-- scaleway/   client, servers, ip_addresses, catalog, partitioning, infrastructure
```

A provider component never saves a Frappe document. It returns typed values, and the caller decides what to record. That keeps provider code testable without a database and keeps persistence in one place.

Metal Server Size stores disk capacity in GiB and price in integer USD cents. The provider fills a missing billing period from the price it does report.

## Host binaries

A build runs only when its source hash changes. The result is published as a public File, because the host fetches it during `install-metald.sh` and holds no Atlas credential. Earlier files stay available, so a host mid-installation is never left without its binary.

## Related

- [docs/providers.md](../docs/providers.md) describes the provider contract and how to add a provider.
- [docs/development.md](../docs/development.md) lists the build tools and manual build commands.
- [server SPEC](../metal_server/SPEC.md) describes the provider interface consumer.
