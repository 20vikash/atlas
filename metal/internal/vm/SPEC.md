# vm: virtual machine manager

[internal SPEC](../SPEC.md) · overview: [docs/vm.md](../../docs/vm.md)

## Purpose

A host operation is slow and can fail. Most VM lifecycle requests must not wait for one. So this package splits a VM in two: a desired record the controller writes, and an observed record the host writes. A lifecycle request stores intent and returns. Reconciliation makes the host agree with it, and retries until it does.

Nothing outside this package writes either record.

## Request flow

```text
API request ---> desired record ---> response returned
                      |
                 Wake  |                 the reconciler runs a pass
                      v
                 reconcile pass ---> Runtime, Network, Storage
                      |
                      v
                 observed record ---> polled by the controller
```

The controller polls until the observed generations match the desired generations. Those generations are the completion signal for asynchronous changes, so every desired change must raise a generation.

## Types

| Type | Owns |
|---|---|
| `Manager` | Both records, the per-VM locks, and every operation. One per daemon. |
| `DesiredRecord` | What the controller asked for. |
| `ObservedRecord` | What the host reached, plus cleanup progress and the last failure. |
| `Information` | The safe view for consumers. Local error detail never reaches it. |
| `WarmImageBuilder` | Warm artifact creation, through narrow capability interfaces. |
| `Runtime`, `Network`, `Storage`, `Snapshots` | Host services, defined here and implemented elsewhere. |

An operation binds the manager to one identifier for its duration. That handle keeps no VM state.

## Records

```text
machines/<id>/config.json   desired
machines/<id>/status.json   observed
```

Generations carry the meaning:

- The desired generation rises only on a real change, so a repeated request does not make the reconciler redo work.
- The restart generation rises per accepted restart, because a restart must happen again even when nothing else changed.
- A pass copies desired generations into the observed record only after the matching work succeeds.

The create fingerprint identifies the reservation a create request asked for. It excludes signed image URLs, so a retry with fresh URLs is recognized as the same request rather than a conflict.

The daemon validates every record at startup and refuses to run on one it cannot read. It never repairs or removes a record: losing desired state is worse than failing to start.

`sleeping` is an observed state only. It means that Firecracker is stopped and a valid VM memory snapshot exists. The `sleep` object stores activity, request, snapshot, and generation values. It stores no paths. Old records without this object remain valid. A partial object or a sleeping record without a published snapshot is invalid.

## Reconciliation

```text
desired running   -> Start or Resume
desired paused    -> Start when necessary, then Pause
desired stopped   -> Stop, or warm Stop to sleeping
desired destroyed -> Remove runtime -> Release network -> Release storage
```

A warm stop changes a live guest to `sleeping`. A manual warm stop has desired state `stopped` with the `warm` flag. Automatic sleep keeps desired state `running`. It checks packet activity before and after the warm stop. New traffic cancels the sleep.

A sleeping VM follows these rules:

| Desired change | Action |
|---|---|
| Running, sleepy, and current | Stay sleeping and arm wake. |
| Running | Restore the snapshot. |
| Paused | Load the snapshot with paused vCPUs. |
| Plain stopped | Discard the snapshot. |
| Warm stopped | Stay sleeping. |
| Restart | Discard the snapshot and cold boot. |
| Incompatible specification | Discard the snapshot and cold boot. |

A restore failure keeps the VM sleeping. An interrupted warm stop recovers from a valid snapshot. An invalid snapshot is an operation failure.

## Packet wake

A sleeping VM wakes on a host-to-guest TCP packet. Automatic sleep arms wake before its final activity check.

```text
TCP packet -> eBPF event -> wake reconciler -> VM lock -> validate
                                                     |
                                                     v
                             restore -> record running -> disarm
```

`WakeFromNetwork` requires the current user ID, desired `running`, `is_sleepy`, observed `sleeping`, and a snapshot generation. A stale or duplicate event has no effect.

A restore failure keeps the VM sleeping and keeps its snapshot. A later pass rearms wake. If restore succeeds but the status write fails, the next pass reads the running runtime and completes the record.

The trigger packet can be lost. Clients must retry. Existing TCP and vsock connections might not survive the restore.

Each pass and desired-state change holds the same VM lock. A wake event waits for an active warm stop before it restores the VM.

The phase and operation ID are written before each host call, so an interrupted pass leaves evidence of what was in flight. A failure stores a safe message for the controller and local detail that stays on the host.

Destroy records progress per resource, so an interrupted destroy resumes instead of repeating released work. Records are removed only after every step succeeds.

## Boundaries

`Runtime`, `Network`, `Storage`, and `Snapshots` are declared here and implemented in other packages. The manager uses those interfaces for host operations and owns the records and reconciliation, not the host details. A runtime starts and inspects a guest. It owns no record, no network identity, no reconciliation, and no cleanup progress.

`WarmImageBuilder` orchestrates: it asks `Manager` for a temporary VM, `WarmRuntime` for the memory snapshot, and `WarmImageStore` to keep the result. A temporary VM has no records on disk and always attempts to release what it created.

## Related

- [docs/vm.md](../../docs/vm.md) gives the lifecycle and the reasons behind this design.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) implements `Runtime`.
- [internal/storage/SPEC.md](../storage/SPEC.md) implements `Storage` and `Snapshots`.
- [internal/network/SPEC.md](../network/SPEC.md) implements `Network`.
- [internal/reconciler/SPEC.md](../reconciler/SPEC.md) drives the passes.
