# Metal architecture

[metal SPEC](../SPEC.md) · packages: [internal SPEC](../internal/SPEC.md)

Metal manages Firecracker virtual machines on one host. Read the [system architecture](../../docs/architecture.md) for the Atlas-to-Metal ownership boundary.

Two ideas explain the rest of the design.

**Metal owns no process.** systemd runs each VM as a unit of the `metal-vm@` template. A metald restart does not stop a guest, and metald rediscovers state by asking systemd and reading disk, never by remembering.

**Metal stores intent, not commands.** The controller writes what it wants. Metal reconciles until the host agrees, and retries until it does. Nothing is held in memory that a restart would lose.

## Components

```text
controller
    |
    v
api ----> vm.Manager ----> firecracker.Runtime ----> systemd ----> jailer ----> Firecracker
 |            |                                      |
 |            |                                      └─ one unit for each VM
 |            ├─ storage stores
 |            └─ network and activity managers
 |
 ├─ host.Service ----> network, image policy, capacity
 └─ wake reconcilers
       ├─ VM desired-state reconciliation
       ├─ network wake reconciliation
       └─ image cache and snapshot staging cleanup
```

`cmd/metald` creates every concrete service. Each consumer declares the small interface it needs, so no package depends on another's implementation. The package map and dependency graph live in [internal/SPEC.md](../internal/SPEC.md).

## State on disk

```text
machines/<id>/config.json   what the controller asked for
machines/<id>/status.json   what the host reached, and cleanup progress
systemd                     whether a VM process runs
ZFS                         images, VM disks, staging, and warm disks
```

Startup validates every record and refuses to run on one it cannot read, because losing desired state is worse than failing to start.

## Read next

- [vm.md](vm.md) the VM lifecycle.
- [storage.md](storage.md) images, disks, and snapshots.
- [networking.md](networking.md) namespaces, egress, and the mesh.
- [api.md](api.md) the controller API.
- [host-layout.md](host-layout.md) where everything lives on the host.
- [testing.md](testing.md) how to bring up a development host.

## Design notes

**Composition root**

- `cmd/metald` creates concrete services in one place.
- Small consumer interfaces keep image, snapshot, VM disk, network, and wake work separate.
- One `base_dir` keeps persistent host files under one root.

**Desired state**

- HTTP handlers return as soon as desired state is stored.
- Reconciliation makes a retry safe after a daemon or host failure.
- Observed state is kept apart from the reservation, because the two change at different times.

**VM start**

- ZFS clones keep disk creation fast.
- systemd owns each Firecracker process, so a daemon restart does not stop guests.
- Warm artifacts are optional. Cold boot remains the fallback.

**Host safety**

- Bearer authentication applies to TCP and Unix listeners.
- The manager holds one operation lock per VM.
- Cleanup progress stays on disk until every owned resource is gone.
