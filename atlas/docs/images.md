# Image lifecycle

Atlas owns public image records and object storage. Metal owns cached host artifacts, virtual machine disks, and temporary snapshot staging.

## System images

The Ubuntu image builder creates one root file system and one kernel. It stores both artifacts, calculates SHA-256 values, and creates or updates an Available System image.

An unchanged build keeps the existing image version. A changed build increases the version after both artifacts are stored.

## Artifact storage

`artifact_storage` says where the bytes of one image are.

| Value | Artifact location | Host URL |
|---|---|---|
| `Object Storage` | The bucket in Atlas Settings, under `vm-images/sha256/<digest>/<name>` | A signed URL that is valid for 24 hours |
| `Site File` | A public site File under `/files/` | A public URL that does not expire |

Atlas has no object storage during bootstrap, so the first System image can use `Site File` storage. Build it with `--storage site-file`. The public URL needs `atlas_base_url` to be reachable by the host. Only a System image can use `Site File` storage, because a public URL gives no tenant separation. The tenant download route refuses a `Site File` image.

## Migration to object storage

Atlas migrates each Available `Site File` image on its own. A change to the object storage fields in Atlas Settings queues one migration for each of them, and a job repeats the search every 15 minutes, which is also what retries a migration that failed. Use the **Migrate to Object Storage** action on the image to start one immediately.

The background job:

1. Uploads each artifact under its content addressed key and compares the stored size with the local size.
2. Saves both object keys, sets `artifact_storage` to `Object Storage`, and commits.
3. Deletes both site Files.

The order keeps one downloadable copy at each step. An interrupted job leaves an unused object or an unused site file, and never an image that a host cannot download. The job is safe to repeat, and it does nothing for an image that is already in object storage. `immutable_reference` uses only the architecture and the artifact digests, so a host that already cached the artifacts does not download them again.

## Machine images

The Create Machine Image action asks Metal to stage a disk and kernel. Metal returns a UUIDv7 snapshot ID. Atlas uses this ID as the image record name.

```text
Metal snapshot staging -> signed multipart parts -> object storage
                       -> validate hashes and sizes -> Available image
                       -> delete Metal staging
```

Atlas saves both multipart upload IDs before it asks Metal to start. A failed start or finalization marks the image Failed and keeps the source Metal Server, snapshot ID, object keys, and upload IDs.

Retry Transfer uses these durable values. It does not create a second image record. Atlas does not log signed URLs.

## Listing

`GET /api/atlas/images` returns the enabled images that the tenant owns, and every enabled System image. A disabled image cannot boot a virtual machine, so the list leaves it out. Retirement disables an image, so a retired image also leaves the list.

Pass `image_type` as `system` or `machine` to return only that type. Omit it to return both. Any other value is a `400`.

`GET /api/atlas/images/<image ID>` returns one image by its identifier, and it also returns a disabled image. Use it to follow a retiring image.

## Retirement and deletion

An Available or Failed image can be retired. Retirement always disables the image, so no new virtual machine can use it.

Atlas reclaims the stored artifacts only when nothing needs them. A Machine image that no virtual machine uses becomes `Deleting` and queues a cleanup job. Every other image becomes `Archived` and keeps its artifacts. A System image is shared, so it always becomes `Archived`.

The cleanup job deletes the root file system and kernel objects, removes the Metal staging data, and then deletes the record.

A scheduled pass reclaims an `Archived` Machine image after its last virtual machine is deleted. The pass moves the image to `Deleting` and queues the same cleanup job. An `Archived` System image stays.

If a Metal or object storage cleanup operation fails, Atlas keeps the image in `Deleting`, records the error, and tries the job again every 30 seconds.

A retiring image keeps its record until the cleanup job removes it, so a caller can read the image by its identifier and follow the status.

## Cached and warm artifacts

Atlas sends enabled Available images with `cache_image` during host synchronization. Metal downloads the root file system and kernel.

A memory snapshot is a host-local warm artifact. It contains disk, memory, and Firecracker state for one exact image and virtual machine shape. Metal never uploads this data to object storage. Cold boot remains the fallback.

See [Metal storage](../../metal/docs/storage.md) for host staging and cleanup.
