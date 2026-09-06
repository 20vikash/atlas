# vm: virtual machine manager

[internal SPEC](../SPEC.md) · overview: [docs/vm.md](../../docs/vm.md)

## Purpose

Package `vm` owns the desired state, the observed state, and VM reconciliation. It uses host services through small interfaces.

## Types

| Type | Role |
|---|---|
| `Manager` | Owns VM records, locks, operations, reconciliation, and cleanup. |
| `DesiredRecord` | Stores the version, reservation, fingerprint, generations, state, and complete specification. |
| `ObservedRecord` | Stores applied generations, state, phase, error, resource data, and cleanup progress. |
| `Runtime` | Controls the VM process and guest operations. |
| `Network` | Makes the complete host network agree with the desired state. |
| `Storage` | Gives disk usage, disk growth, and disk release operations. |
| `Snapshots` | Stages a root file system and a kernel. |
| `Specification` | Contains compute, disk, image, network, and guest data. |
| `Information` | Combines safe desired data and observed data for consumers. |
| `WarmImageBuilder` | Creates optional warm artifacts through narrow capability interfaces. |

The daemon creates one `Manager`. A temporary `machine` value does not keep a record or an API client.

`ManagerConfig.FastApplyTimeout` limits an immediate metadata operation. The default value is 2 seconds.

## Records

```text
machines/<id>/config.json   desired state
machines/<id>/status.json   observed state
```

Both records use schema version `1`. The manager rejects unknown fields, extra JSON values, invalid states, and unsupported versions.

The daemon does a check of all records during startup. It does not change or remove an incompatible record.

The create fingerprint is a SHA-256 value. It does not include signed image URLs.

The desired generation increases only when desired data changes. The restart generation increases for each accepted restart request.

## Reconciliation

```text
desired running   -> Start or Resume
desired paused    -> Start when necessary, then Pause
desired stopped   -> Stop
desired destroyed -> Remove runtime -> Release network -> Release storage
```

The manager stores the phase and operation ID before each host operation. A failure record has a safe message and local detail.

Runtime, network, and storage cleanup have separate progress values. The manager removes the VM directory after all cleanup operations succeed.

## Runtime boundary

The `Runtime` interface has these operations:

```text
Inspect
Start
Stop
Pause
Resume
Remove
RefreshMetadata
RefreshDisk
ConnectSSH
```

The runtime does not own VM records, network identities, reconciliation, or cleanup progress.

## Warm-image boundary

`WarmRuntime` supplies Firecracker compatibility and memory snapshot operations. `WarmImageStore` finds, promotes, and removes warm artifacts.

`WarmImageBuilder` owns the orchestration. It asks `Manager` to run a temporary VM and then promotes the completed artifact.

## Related

- [docs/vm.md](../../docs/vm.md) gives the VM lifecycle.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) describes the Firecracker runtime.
- [internal/storage/SPEC.md](../storage/SPEC.md) describes disks and snapshots.
- [internal/network/SPEC.md](../network/SPEC.md) describes host networks.
