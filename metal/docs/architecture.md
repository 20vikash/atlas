# Metal architecture

[metal SPEC](../SPEC.md) · packages: [internal SPEC](../internal/SPEC.md)

Metal manages Firecracker virtual machines on one host. It stores host state on disk and keeps no VM registry in memory.

Read the [system architecture](../../docs/architecture.md) for the Atlas-to-Metal ownership boundary.

## Components

```text
controller
    |
    v
api ----> vm.Manager ----> firecracker.Runtime ----> systemd ----> jailer ----> Firecracker
 |            |                                      |
 |            |                                      └─ one unit for each VM
 |            ├─ storage stores
 |            └─ network manager
 |
 └─ wake reconcilers
       ├─ VM desired-state reconciliation
       └─ image cache and snapshot staging cleanup
```

`cmd/metald` creates all concrete services. Consumer packages define the small interfaces that they use.

## Layering

`vm.Manager` owns records, operations, reconciliation, and cleanup. `firecracker.Runtime` owns Firecracker process and guest operations.

The storage, network, systemd, and Firecracker API packages own host integration. See [internal/SPEC.md](../internal/SPEC.md) for the package graph.

## Stateless design

```text
machines/<id>/config.json   versioned reservation and desired state
machines/<id>/status.json   versioned observed state, operation data, and cleanup progress
systemd                     Firecracker process state
ZFS                         images, VM disks, staging, and warm disks
```

A metald restart does not stop a VM. Startup rejects an incompatible or damaged VM record.

## Request flow

Create and lifecycle requests are asynchronous.

```text
HTTP request
   -> save the desired state
   -> wake the VM reconciler
   -> return 202
   -> reconcile host state
```

Poll `GET /v1/vms/{id}` until the desired and observed generations match. The first observed state can be `unknown`.

## systemd unit model

Systemd owns each `metal-vm@<id>.service` process. Metal controls the unit through D-Bus and uses the Firecracker Unix socket for guest operations.

Metal dials a short socket link in `/run/metal`. This avoids the Unix socket path limit inside the jail.

## Storage and warm boot

Normal VM disks are ZFS clones of an image `@ready` snapshot. This keeps cold VM creation fast.

The image reconciler can create host-local warm artifacts for a cached image. Warm artifacts require an exact image, CPU, memory, disk, architecture, and Firecracker match.

If warm boot fails, Metal removes the attempted VM disk and uses cold boot.

## Network

Each VM uses one Linux network namespace and one `tap0` device. `Network.Ensure` applies the complete desired network state.

`POST /v1/sync` also applies the managed WireGuard peer set for the host.

## API access

All `/v1` routes require a bearer token. The configuration stores the lowercase SHA-256 digest of that token.

## Design notes

**Composition root**

- `cmd/metald` creates concrete services in one place.
- Small consumer interfaces keep image, snapshot, VM disk, and network work separate.
- One `base_dir` keeps persistent host files under one root.

**Desired state**

- HTTP handlers return quickly after they save desired state.
- Reconciliation makes retries safe after daemon or host operation failures.
- `status.json` keeps observed state separate from reservation metadata.

**VM start**

- ZFS clones keep disk creation fast.
- Systemd owns each Firecracker process, so daemon restart does not stop guests.
- Warm artifacts are optional. Cold boot remains the fallback.

**Host safety**

- Bearer authentication applies to TCP and Unix listeners.
- The manager uses one operation lock for each VM.
- Cleanup progress remains on disk until all owned resources are gone.

## Read next

- [docs/api.md](api.md) lists the HTTP API.
- [docs/vm.md](vm.md) describes VM lifecycle behavior.
- [docs/storage.md](storage.md) describes images and disks.
- [docs/snapshots.md](snapshots.md) describes image staging and warm artifacts.
- [docs/networking.md](networking.md) describes host networking.
- [docs/host-layout.md](host-layout.md) lists host paths.
