# Storage

[metal SPEC](../SPEC.md) · detail: [internal/storage/SPEC.md](../internal/storage/SPEC.md)

Metal uses ZFS for image volumes and virtual machine disks. A VM disk is a copy-on-write clone of an image snapshot, so reserving a disk costs almost nothing.

An image reference is bound to its content. The same name with different digests is a conflict, never an update.

Datasets, image import, disk lifecycle, pruning, and warm artifacts: [internal/storage/SPEC.md](../internal/storage/SPEC.md). Host paths: [host-layout.md](host-layout.md).

## Two kinds of snapshot

Metal uses the word snapshot for two unrelated flows.

| | Machine image snapshot | Warm artifact |
|---|---|---|
| Contains | A disk and a kernel | A disk, guest memory, and Firecracker state |
| Purpose | Transfer an image to Atlas | Skip boot on this host |
| Leaves the host | Yes, by multipart upload | Never |
| Restores its source VM | No | Not applicable |

A Machine image snapshot is not a rollback point. To use one, Atlas creates a VM from its image reference. Metal has no public restore or promote operation, because Atlas owns image records and object storage.

## Throughput and IOPS limits

The VM configuration can set `disk.throughput_mibps` and `disk.iops`. Each limit covers reads and writes together, and `0` means unlimited.

Metal applies them as Firecracker drive rate limiters, one token bucket each, changed on a live VM without a restart.

Metal does not use the cgroup IO controller. ZFS schedules its own IO through the ARC and the transaction group pipeline, so a block-level limit does not hold for a dataset.

## Design notes

- ZFS clone creation keeps VM disk reservation fast.
- Immutable manifests prevent one image reference from changing content.
- Grow-only resize avoids host-side file system shrink risk.
- Separate stores keep pool, VM disk, image, and staging state with one owner.
- Image policy controls retention, not whether a VM can download an image.
- Public snapshots create new immutable images instead of destructive rollback points.
- Metal generates the snapshot ID, so Atlas and Metal share one transfer identity.
- A read-only staging clone gives the uploader a stable root file system.
- Upload activity extends staging life, so a transfer can be retried safely.
- Guest memory stays on the host, which keeps transfers small and avoids an unsafe memory restore elsewhere.
