# Atlas app specification

[Root specification](../SPEC.md)

[Module specification](atlas/SPEC.md)

## Purpose

The Atlas app uses Frappe to manage provider hosts, virtual machines, images, and user actions.

## Layout

```text
atlas/                         Site settings, provider behavior, and host binary builds
  core/                        Provider clients and the host binary builder
  doctype/                     Atlas Settings
server/                        Provider hosts and server catalog records
  core/                        Provisioning, host installation, disk inventory, and catalog sync
  doctype/                     Server records and catalog DocTypes
vm/                            Virtual machine records, images, and orchestration
  core/                        Placement, Metal transport, and image movement
  doctype/                     Virtual Machine and Virtual Machine Image
realtime/                      Browser console bridge
scripts/                       Host installation scripts
```

## Software

The app uses Python 3.14, Frappe, MariaDB, Redis, Node, and Yarn.

## Scope

Atlas stores settings, server catalogs, image metadata, and virtual machine request metadata. Metal owns each virtual machine runtime and desired state. Atlas does not store a virtual machine lifecycle state machine.

The Virtual Machine name is the Metal VM ID. Creation uses idempotent `PUT /v1/vms/{name}` and accepts HTTP `202`. Atlas uses `GET /v1/vms/{name}` after a lost response. Atlas keeps the draft if the result is uncertain.

Atlas exchanges WireGuard peers, desired cached images, and host capacity with `POST /v1/sync`. Placement uses a fresh capacity sample and the image architecture. It locks the candidate Server and subtracts requests that the sample does not include.

Virtual Machine Image is the durable boot artifact for System and Machine images. Each record owns rootfs and kernel objects, exact sizes, and SHA-256 values. Machine image transfer behavior is documented in [the VM module SPEC](vm/SPEC.md).

Use [the virtual machine control-plane guide](docs/vm-control-plane.md) for request and retry boundaries. Use [the image guide](docs/images.md) for System and Machine image lifecycles. Use [Atlas operations](docs/operations.md) for fault recovery.

## Validation

See [docs/development.md](docs/development.md) for the commands to run.

## Module specifications

- [Atlas settings](atlas/SPEC.md)
- [Servers](server/SPEC.md)
- [Virtual machines](vm/SPEC.md)
- [Realtime console bridge](realtime/SPEC.md)

## Ownership

Keep provider behavior in `atlas/core/server_providers/`. Keep settings behavior in `atlas/doctype/`.

Keep server orchestration in `server/core/`. Keep DocType controllers as lifecycle and API boundaries.

Keep virtual machine orchestration and image transfers in `vm/core/`.
