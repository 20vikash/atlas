# internal: metald packages

[metal SPEC](../SPEC.md) · overview: [docs/architecture.md](../docs/architecture.md)

## Purpose

`internal/` contains the Metal runtime packages. `cmd/metald` creates concrete services and injects them into consumers.

The `vm` package defines the contracts. The other packages implement or consume those contracts.

## Packages

| Package | Role |
|---|---|
| [vm](vm/SPEC.md) | VM manager, records, reconciliation, interfaces, and value types. |
| [firecracker](firecracker/SPEC.md) | Firecracker runtime and optional warm capability. |
| [firecracker/api](firecracker/api/SPEC.md) | Firecracker REST client over a Unix socket. |
| [api](api/SPEC.md) | Authenticated HTTP API. |
| `host` | Host synchronization and capacity. |
| [console](console/SPEC.md) | VM serial consoles over PTYs. |
| [reconciler](../docs/architecture.md) | VM state, image cache, warm artifact, and staging cleanup loops. |
| [storage](storage/SPEC.md) | ZFS pool, VM disks, images, and snapshot staging. |
| [network](network/SPEC.md) | VM namespaces and managed WireGuard peers. |
| [platform](platform/SPEC.md) | Host files, commands, and systemd control. |

## Dependency graph

```text
cmd/metald
   ├─ api -> vm.Manager, host.Service, console.SerialBroker
   ├─ reconciler -> vm.Manager
   ├─ firecracker -> vm, storage, platform, console, firecracker/api
   ├─ storage -> vm, platform
   └─ network -> vm, platform
```

The `vm` package defines small host service interfaces. The `vm.Manager` owns VM state, operations, and warm-image orchestration.

## Related

- [metal SPEC](../SPEC.md) routes to concept documents.
- [cmd/metald/SPEC.md](../cmd/metald/SPEC.md) describes runtime construction.
- [docs/architecture.md](../docs/architecture.md) gives the broad architecture.
