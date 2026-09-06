# Atlas app specification

[Root specification](../SPEC.md)

[Module specification](atlas/SPEC.md)

## Purpose

The Atlas app uses Frappe to manage provider hosts, virtual machines, images, and user actions.

## Layout

```text
atlas/                         Site settings, provider behavior, TLS, and host binary builds
  core/                        Provider clients, TLS issuance, and the host binary builder
  doctype/                     Atlas Settings and SSH Task
metal_server/                   Provider hosts and Metal Server catalog records
  core/                        Provisioning, host installation, disk inventory, and catalog sync
  doctype/                     Metal Server records and catalog DocTypes
service/                       Atlas services that run on virtual machines
  core/                        HTTP proxy packaging, provisioning, and configuration
  doctype/                     Proxy Server
vm/                            Virtual machine records, images, and orchestration
  core/                        Placement, Metal transport, and image movement
  doctype/                     Virtual Machine and Virtual Machine Image
realtime/                      Browser console bridge
scripts/                       Host installation scripts
```

## Software

The app uses Python 3.14, Frappe, MariaDB, Redis, Node, and Yarn.

## Scope

Atlas stores settings, Metal Server catalogs, image metadata, virtual machine request metadata, and SSH task logs. Metal owns each virtual machine runtime and desired state. Atlas does not store a virtual machine lifecycle state machine.

The Virtual Machine name is the Metal VM ID. Creation uses idempotent `PUT /v1/vms/{name}` and accepts HTTP `202`. Atlas uses `GET /v1/vms/{name}` after a lost response. Atlas keeps the draft if the result is uncertain.

Atlas exchanges WireGuard peers, desired cached images, and host capacity with `POST /v1/sync`. Placement uses a fresh capacity sample and the image architecture. It locks the candidate Metal Server and subtracts requests that the sample does not include.

Virtual Machine Image is the durable boot artifact for System and Machine images. Each record owns rootfs and kernel objects, exact sizes, and SHA-256 values. Machine image transfer behavior is documented in [the VM module SPEC](vm/SPEC.md).

Atlas holds one wildcard TLS certificate for the region. A wildcard name can only be proved through DNS, so issuance uses the ACME dns-01 challenge and the configured DNS provider. See [the wildcard TLS guide](docs/wildcard-tls.md).

Use [the virtual machine control-plane guide](docs/vm-control-plane.md) for request and retry boundaries. Use [the image guide](docs/images.md) for System and Machine image lifecycles. Use [Atlas operations](docs/operations.md) for fault recovery.

## Validation

See [docs/development.md](docs/development.md) for the commands to run.

## Module specifications

- [Atlas settings](atlas/SPEC.md)
- [Metal Servers](metal_server/SPEC.md)
- [Services](service/SPEC.md)
- [Virtual machines](vm/SPEC.md)
- [Realtime console bridge](realtime/SPEC.md)

## Ownership

Keep provider behavior in `atlas/core/server_providers/`. Keep settings behavior in `atlas/doctype/`.

Keep certificate issuance in `atlas/core/tls/`.

Keep Metal Server orchestration in `metal_server/core/`. Keep DocType controllers as lifecycle and API boundaries.

Keep service packaging and installation in `service/core/`.

Keep virtual machine orchestration and image transfers in `vm/core/`.
