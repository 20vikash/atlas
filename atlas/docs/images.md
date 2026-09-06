# Image lifecycle

Atlas owns public image records and object storage. Metal owns cached host artifacts, virtual machine disks, and temporary snapshot staging.

## System images

The Ubuntu image builder creates one root file system and one kernel. It uploads both objects, calculates SHA-256 values, and creates or updates an Available System image.

An unchanged build keeps the existing image version. A changed build increases the version after both objects upload.

## Machine images

The Create Machine Image action asks Metal to stage a disk and kernel. Metal returns a UUIDv7 snapshot ID. Atlas uses this ID as the image record name.

```text
Metal snapshot staging -> signed multipart parts -> object storage
                       -> validate hashes and sizes -> Available image
                       -> delete Metal staging
```

Atlas saves both multipart upload IDs before it asks Metal to start. A failed start or finalization marks the image Failed and keeps the source Server, snapshot ID, object keys, and upload IDs.

Retry Transfer uses these durable values. It does not create a second image record. Atlas does not log signed URLs.

## Cached and warm artifacts

Atlas sends enabled Available images with `cache_image` during host synchronization. Metal downloads the root file system and kernel.

A memory snapshot is a host-local warm artifact. It contains disk, memory, and Firecracker state for one exact image and virtual machine shape. Metal never uploads this data to object storage. Cold boot remains the fallback.

See [Metal snapshots](../../metal/docs/snapshots.md) for host staging and cleanup.
