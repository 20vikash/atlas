# Atlas app

The Atlas Frappe app owns provider integration, Metal Server records, placement, images, and user actions.

Atlas sends desired virtual machine state to Metal. Metal owns runtime state and host resources.

## Start here

1. Read the [system architecture](../docs/architecture.md) to understand the Atlas and Metal boundary.
2. Read the [Atlas app specification](SPEC.md) for app ownership and layout.
3. Choose an area below, then read its module SPEC before you change its code.

| Area | Purpose | Start here |
|---|---|---|
| Atlas settings | Provider selection, credentials, region data, and host binaries | [Atlas module](atlas/SPEC.md) |
| Metal Servers | Provider hosts, Metal installation, and capacity | [Metal Server module](metal_server/SPEC.md) |
| Virtual machines | Placement, Metal requests, images, and user workflows | [Virtual machine module](vm/SPEC.md) |
| Realtime | Console WebSocket bridge | [Realtime specification](realtime/SPEC.md) |

## Guides

- [Provider contract and extension guide](docs/providers.md)
- [Metal Server lifecycle](docs/metal-server-lifecycle.md)
- [Virtual machine control plane](docs/vm-control-plane.md)
- [Image lifecycle](docs/images.md)
- [Atlas operations](docs/operations.md)
- [Development and tests](docs/development.md)
- [System architecture](../docs/architecture.md)
- [Metal `/v1` contract](../docs/metal-v1-contract.md)

## Boundaries

Keep DocType methods as permission and API boundaries. Put provider behavior in `atlas/core/server_providers/`.

Put Metal Server setup behavior in `metal_server/core/`. Put virtual machine orchestration in `vm/core/`.

Do not store mutable Metal runtime state in DocType fields.
