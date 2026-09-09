# Virtual machine module specification

[Atlas app specification](../SPEC.md)

For the control-plane overview, see [docs/vm-control-plane.md](../docs/vm-control-plane.md).

## Purpose

Atlas stores what was requested. Metal stores what is running. This module keeps that line: a Virtual Machine record holds the reservation and the request, and every runtime value is read through to Metal instead of copied into a field.

The record name is the Metal VM ID. That single choice makes create idempotent, because a retry addresses the same resource.

## Types

| Type | Owns |
|---|---|
| `VirtualMachine` (DocType) | The reservation, permissions, and the whitelisted API. Runtime values read through. |
| `VirtualMachineImage` (DocType) | The durable boot artifact and its immutable reference. |
| `VirtualMachineService` | Every operation that spans an Atlas record and a Metal host. |
| `PlacementService` | Choosing and locking the host for a new VM. |
| `MetalClient` | `/v1` HTTP transport and error classification. |
| `metal_models` | Typed read views of Metal responses. |
| `VirtualMachineCreateRequest` | Validated create input. |
| `vm_image_transfer`, `multipart_upload`, `image_builder` | Machine image movement and System image creation. |
| `reconciliation` | Settling records whose Metal outcome was never confirmed. |

## Create

```text
validate request
  -> select and lock a Metal Server        capacity sample minus local reservations
  -> insert draft, COMMIT            the draft reserves capacity and fixes the name
  -> PUT /v1/vms/{name}              idempotent on the name
  -> clear draft
```

The commit before the Metal call is deliberate. The draft must exist on disk before Atlas asks Metal for anything, so a lost response leaves a record to reconcile rather than an orphaned VM.

An uncertain response keeps the draft. Only a Metal `404` deletes it.

## Placement

Capacity comes from a Metal Server Usage sample, which Metal produced at some earlier moment. Placement subtracts every VM created after that sample and every uncertain draft, so a burst of requests cannot spend the same reported capacity twice.

The chosen Metal Server row is locked and its capacity rechecked before the draft is inserted. The lock is released by the commit, before Atlas calls Metal, so a slow host never holds a row.

A sample older than the freshness limit is not used. Placement reports a synchronization fault instead of guessing.

## Reading state

A Virtual Machine property calls Metal once per request and caches the result. A draft or a VM that Metal reports as absent reads as `unknown`. Any other Metal failure is raised, so a read fault is never shown as a state.

## Reconciliation

Termination detaches the public IPv4 address and keeps its tenant reservation. The record stays until Metal confirms the VM is absent.

Two scheduled jobs settle records that a lost response left uncertain: stale drafts and terminating VMs. Both ask Metal the same question and share one settle path. Metal is the authority: a confirmed absence deletes the record, and any other failure is logged and left alone, because an unreachable host says nothing about whether the VM exists.

## Images

Virtual Machine Image is the durable boot artifact. `image_type` is `System` or `Machine`. Each image carries a tenant ID, and a Machine image inherits the tenant of its source virtual machine. System images are shared with every tenant. Machine images are visible only to their owning tenant. Each image has its own rootfs and kernel object key, exact byte size, and SHA-256 value. The immutable reference uses the architecture and both artifact hashes.

Only enabled, Available images can create VMs. Atlas sends enabled, Available images with `cache_image` to each host through `POST /v1/sync`. Signed URLs are valid for 24 hours.

The policy is:

| Cache Image | Memory Snapshot | Host behavior |
|---|---|---|
| No | No | Download on demand. |
| Yes | No | Retain the rootfs and kernel. |
| No | Yes | Use a normal cold boot. |
| Yes | Yes | Retain the artifacts and build a local warm snapshot. |

Atlas never uploads memory or Firecracker state to object storage. A memory snapshot needs an explicit CPU, memory, and disk configuration. Metal boots a local template for about 5 minutes. It keeps the disk, state, and memory on that host. Only an exact VM shape can use these artifacts.

## Machine image transfer

The Create Machine Image action calls `POST /v1/vms/{id}/snapshots`. Metal returns a UUIDv7 for local staging. Atlas uses it as the Virtual Machine Image name. A background job then:

1. Creates separate multipart uploads for `images/{image-id}/rootfs.img` and `images/{image-id}/kernel`.
2. Signs 2 GiB upload parts for 24 hours.
3. Calls `POST /v1/snapshots/{snapshot_id}/upload`.
4. Verifies the sizes, SHA-256 values, part numbers, ETags, and final stored object sizes.
5. Completes both uploads and deletes the local staging data.

The image record keeps the source server, upload IDs, status, and errors. Atlas saves the upload IDs before it asks Metal to start. A start or finalization error marks the image as Failed and keeps the retry values. Retry Transfer uses these values. `source_virtual_machine` is audit text only. Metal deletes staging after 48 hours without activity.

## Machine image deletion

`vm_image_deletion.py` owns Machine image removal. The request marks the image `Deleting`, disables it, and queues a repeatable cleanup job. Atlas refuses the request while a virtual machine uses the image.

The job aborts every incomplete multipart upload, deletes both stored objects, removes the remaining Metal staging data, and then deletes the record. If a Metal or object storage cleanup operation fails, Atlas keeps the image in `Deleting`, records the error, and queues it again every 30 seconds.

## System image publisher

Build and publish a pinned Ubuntu image:

```sh
bench --site <site> atlas build-ubuntu-base-image --version 24.04 --architecture amd64
```

`image_builder.py` owns the build and publication behavior. The CLI command only validates its options and connects to each selected site. The publisher creates an Available System image and stores exact artifact sizes.

## SSH keys

Atlas can replace the complete VM SSH key list without storing it in the Virtual Machine DocType. Metal saves the list and updates MMDS for an active VM. The base image `AuthorizedKeysCommand` reads the current MMDS keys during each login.

## Privileged VMs

Atlas WG Mesh reserves tenant 0 for the privileged tenant. A privileged VM crosses tenants, so `before_insert` refuses a privileged VM on another tenant.

The whitelist is host state that every host shares. `atlas.server.usage` sends the complete set of live privileged VM addresses to each host with `POST /v1/sync`.

Use `Grant Privilege` and `Revoke Privilege` under `Dangerous Actions` to change the flag. Each host applies the change on its next sync, so it takes up to 30 seconds.

## Per-VM metadata

The image is shared, so nothing per VM can be baked into it. `atlas-metadata.service` reads MMDS and applies the hostname and the mesh address. It writes a systemd-networkd drop-in with the address and the `fdaa::/16` route, then reloads networkd. Each step does nothing when the value already matches.

`atlas-metadata.timer` repeats the unit every 10 seconds. A warm snapshot resumes with new MMDS content, and systemd does not start a unit again after a resume, so a warm VM needs the repeat to receive its own hostname and address. cloud-init uses `preserve_hostname`, so the unit is the only owner of the hostname.

## Custom metadata

Atlas stores custom VM metadata as key-value rows and sends it to Metal. Guests read each value from `latest/meta-data/attributes/<key>`.

The Ubuntu image builder installs the Atlas cloud-init datasource. See [`build_ubuntu_server_image.sh`](scripts/build_ubuntu_server_image.sh).

## Mesh address

Atlas derives the Atlas WG Mesh address for a VM from the region ID, the tenant ID, and the VM number, and sends it as `network.wireguard_mesh_ipv6`. Metal registers the address on the host and publishes it at `latest/meta-data/mesh-ipv6`.

`atlas-metadata.service` in the guest applies it. See [Per-VM metadata](#per-vm-metadata).

## Network changes

Metal owns the VM network state. Atlas reads the typed desired state and changes one value. Atlas then sends the complete network object.

| Action | Behavior |
|---|---|
| Attach IP Address | Sets an attach intent and sends the address with `uplink` egress. |
| Detach IP Address | Sends an empty address and sets a detach intent. |
| Edit Network Throughput | Sends private and public limits in MiB/s. `0` removes a limit. |
| Change Egress Mode | Sends `uplink`, `mesh`, or `none`. |

Atlas applies the Metal change before it releases an address. A VM can hold one public IPv4 address. Public IPv4, egress, and throughput limits can also be set during creation.

Egress controls internet reachability. It does not control mesh reachability.

| Mode | VM can reach | Public IPv4 | Throughput limits |
|---|---|---|---|
| `uplink` | mesh peers and the internet | allowed | private and public |
| `mesh` | mesh peers only | rejected | private applied, public stored |
| `none` | nothing | rejected | none |

A public IPv4 address needs `uplink`. Atlas refuses `mesh` and `none` while an address is attached.

Active connections can stop when the public IPv4 address or the egress mode changes.

## Sleep policy

Metal can put an idle VM to sleep and wake it on the next network packet. The policy is per VM: `is_sleepy` allows automatic sleep, and `idle_timeout_seconds` is the idle time before sleep. An idle timeout of `0` disables sleep.

The policy is intent, not machine shape, so Metal accepts a change in any state. Atlas reads both values through to Metal and sets them with the Edit Sleep Policy action or during creation.

`PUT /v1/vms/{name}/compute` replaces the complete compute object, so Atlas reads the desired compute object, applies the requested change, and sends every value. A vCPU or memory change still needs a stopped VM.

## Disk limits

The VM configuration can set `disk_throughput_mibps` and `disk_iops`. Each limit covers reads and writes together. A value of `0` does not apply a limit.

The Edit Disk Limits action sends the size and both limits with `PUT /v1/vms/{name}/disk`. Metal applies the change through reconciliation.

## Termination and deletion

The Terminate action sends `DELETE /v1/vms/{name}`. Atlas keeps the request metadata. Delete the document after Metal confirms that the VM is absent.

## Related

- [docs/vm-control-plane.md](../docs/vm-control-plane.md) request and retry boundaries.
- [docs/images.md](../docs/images.md) System and Machine image lifecycles.
- [metal/internal/vm/SPEC.md](../../metal/internal/vm/SPEC.md) the other side of the boundary.
- [docs/metal-v1-contract.md](../../docs/metal-v1-contract.md) the wire contract.
