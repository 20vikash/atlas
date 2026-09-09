# Metal Component Specification

[Root specification](../SPEC.md)

## Purpose

Metal manages virtual machines on a host. Its executable is `metald`.

## Layout

```text
cmd/metald/                  metald executable
internal/api/                HTTP API
internal/console/            VM serial consoles over PTYs
internal/firecracker/        Firecracker support
internal/host/               Controller synchronization and host capacity
internal/network/            Linux VM networking, packet activity, and WireGuard peer management
internal/reconciler/         VM and image reconciliation
internal/storage/            ZFS images, VM disks, and pool capacity
internal/platform/           Host file, command, and systemd support
internal/vm/                 VM domain logic
scripts/                     host bootstrap scripts
Makefile                     build metald into dist/
docs/                        concept docs and references
test/                        Integration test data
```

Most packages have a `SPEC.md`. Start at [`internal/SPEC.md`](internal/SPEC.md) for
the package map and dependency graph, or [`cmd/metald/SPEC.md`](cmd/metald/SPEC.md)
for the daemon.

## Software

Metal uses Go 1.26.2, Echo, Firecracker, systemd, dbus, ZFS, and Linux host features.

## Module

The module path is `github.com/frappe/atlas/metal`. Run Go commands from `metal/`.

## Validation

See [`docs/development.md`](docs/development.md) for the commands to run.

## Documentation

`docs/` gives the overall guide and the host-facing references. Package detail lives in each package's `SPEC.md`; start at [`internal/SPEC.md`](internal/SPEC.md).

| Document | Purpose |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | The big picture. Read this first. |
| [`docs/host-layout.md`](docs/host-layout.md) | Every host path and dataset. |
| [`docs/testing.md`](docs/testing.md) | Bring up a development host and run the integration test. |
| [`docs/development.md`](docs/development.md) | Local checks and where changes belong. |
| [`docs/operations.md`](docs/operations.md) | Fault checks and safe recovery. |
| [`docs/vm.md`](docs/vm.md) | VM lifecycle and design rationale. |
| [`docs/storage.md`](docs/storage.md) | Storage model and design rationale. |
| [`docs/networking.md`](docs/networking.md) | Network model and design rationale. |
| [`docs/api.md`](docs/api.md) | Controller API model and design rationale. |

## Scope

Metal manages host VMs. It does not define the proxy or WG Mesh services.

## Ownership

Keep VM logic in `internal/vm/`. Keep HTTP handlers thin. `internal/network/` owns VM network convergence, traffic tracking, WireGuard peer reconciliation, and persistent peer state. `internal/storage/` separates the ZFS pool, VM disks, images, and snapshot staging. `internal/reconciler/` owns the asynchronous VM and image loops.
