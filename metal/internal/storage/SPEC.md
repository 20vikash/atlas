# storage: ZFS images and VM disks

[internal SPEC](../SPEC.md) · overview: [docs/storage.md](../../docs/storage.md)

## Purpose

Package `storage` imports images, creates fast virtual machine disk clones, manages warm artifacts, and stages machine image uploads.

## Types

| Type | State and responsibility |
|---|---|
| `ZFSPool` | Pool name, dataset names, device paths, and capacity. |
| `VirtualMachineStore` | VM disk preparation, usage, growth, and release. |
| `ImageStore` | Image directory, HTTP client, image locks, manifests, policy, pruning, and warm artifacts. |
| `SnapshotStore` | Snapshot directory, HTTP client, snapshot locks, staging, upload, deletion, and pruning. |
| `Stores` | The four services created by `NewStores`. |

Consumers define the interfaces that they need. The storage package does not export one broad storage interface.

Files group behavior by resource, so changes to disks, images, or snapshots stay focused.

## Dataset layout

```text
ZFS
<pool>/images/<image>@ready       VM clone source
<pool>/vms/<vm-id>                VM disk
<pool>/staging/<snapshot-id>      read-only upload source
<pool>/warm/<key>@ready           warm boot disk

files
images/<image>/manifest.json
images/<image>/vmlinux
images/<image>/boot-args
images/<image>/last-used
images/<image>/warm/<key>/state
images/<image>/warm/<key>/memory
snapshots/<id>/metadata.json
snapshots/<id>/vmlinux
image-policies.json
```

## Clone and snapshot lineage

```text
image @ready -> VM clone -> temporary staging or warm source snapshot
```

VM clones share image blocks. Staging clones share one fixed VM snapshot. Warm promotion uses ZFS send and receive for an independent warm disk.

## Image import

`EnsureImage` verifies the architecture, URLs, and SHA-256 values. An image reference cannot identify different content.

Metal downloads with bounded retries, creates a ZFS volume, copies the root file system, and creates `@ready`. It stores the kernel and manifest in the image directory.

## Provision flow

`PrepareBoot` ensures the image, links the kernel, clones the VM disk when absent, grows it when required, and creates the block node in the jailer chroot.

`PrepareRootFileSystem` performs disk preparation without the kernel link. `Release` removes the VM dataset and its snapshots.

## Image policy

`SetImagePolicies` atomically replaces the policy file. The reconciler downloads retained images and prunes the rest after the idle period it sets: [internal/reconciler/SPEC.md](../reconciler/SPEC.md).

A successful VM start records image use. Metal keeps an image when a dependent VM clone prevents deletion.

## Snapshot and image operations

`Stage` creates a UUIDv7 value and stages a VM disk and kernel for the VM manager.

`StartUpload` validates that the parts cover the artifact exactly and starts an asynchronous upload. The part size is fixed, because the controller signs each part against it. Uploads use a store-owned root context and wait group. Shutdown cancels new and active uploads and waits up to the daemon deadline.

Durable upload state lives in the staging metadata. Live byte progress stays in memory. An upload recorded as running with no goroutine behind it did not survive a restart, so `UploadStatus` reports it as pending and the controller starts it again.

`DeleteSnapshot` cancels a running upload and waits for it to stop before it removes the data that upload is reading. It removes the staging clone before the source snapshot, because ZFS keeps a snapshot alive while a clone of it exists.

## Concurrency

Image imports and snapshot staging are serialized per resource, not per store. One lock covers an image reference, and one covers a snapshot ID, so unrelated images and snapshots proceed at the same time.

Every multi-step create attempts to remove what it made when a later step fails. An image, a staged snapshot, and a warm image are complete or absent when cleanup succeeds. Deletes accept an already absent resource, so a retry after a partial failure still succeeds.

## Warm artifacts

The warm key includes image identity, exact VM shape, and Firecracker compatibility. `ImageStore` keeps state, memory, and an independent warm disk snapshot.

Memory and Firecracker state never leave the host. Warm artifacts are not public VM restore points.

## Chroot materialization

Metal links the kernel into the jailer chroot and creates a block node for the VM volume. `LinkOrCopy` avoids a full memory-file copy when the filesystem supports links or reflinks.

## Related

- [docs/storage.md](../../docs/storage.md) gives the broad storage model.
- [docs/snapshots.md](../../docs/snapshots.md) describes staging and warm artifacts.
- [internal/vm/SPEC.md](../vm/SPEC.md) coordinates VM operations.
- [docs/host-layout.md](../../docs/host-layout.md) lists files and datasets.
