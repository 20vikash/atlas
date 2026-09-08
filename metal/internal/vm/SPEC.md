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

`sleeping` is an observed state only. A controller cannot request it. A warm stop and automatic sleep both reach it: the state means no Firecracker process, a valid memory snapshot, and a start that resumes. The observed record carries a `sleep` object with safe times and generation numbers: `eligible_at`, `requested_at`, `snapshot_generation`, `snapshot_created_at`, and `last_network_activity_at`. It never carries an artifact path, because the Firecracker runtime derives every path from the VM ID. The object is additive, so an old record with no `sleep` key still loads. The reader rejects a partial or corrupt `sleep` object instead of repairing it. A `sleeping` record must carry a published snapshot generation.

## Reconciliation

```text
desired running   -> Start or Resume
desired paused    -> Start when necessary, then Pause
desired stopped   -> Stop, or warm Stop to sleeping
desired destroyed -> Remove runtime -> Release network -> Release storage
```

A VM enters `sleeping` from a live guest by a warm stop. A manual warm stop uses a `stopped` desired state with the `warm` flag. Automatic sleep keeps the desired state running and warm-stops an idle sleepy VM after the idle timeout. An automatic sleep samples network activity before and after the warm stop and aborts, restoring the VM, when a packet arrives during the stop.

A sleeping VM holds asleep while its desired record still wants sleep and its generation is caught up. Any other desired state resumes the VM from the snapshot to running. A later pass then applies a stop or pause. A resume failure keeps the VM sleeping and never cold boots, so the in-memory guest is not lost. A warm stop that terminates the process before it records `sleeping` recovers to `sleeping` from the valid snapshot on the next pass, and reports a failure for an invalid snapshot.

## Packet wake

A sleeping VM can wake on a host-to-guest packet. Automatic sleep arms the wake before the final idle check, so a packet that arrives during the warm stop produces a wake event that waits for the VM lock. The event reaches `WakeFromNetwork` through the network monitor and the wake reconciler. This is a warm VMM restart, not a guest reboot: the guest resumes from its memory snapshot.

`WakeFromNetwork` takes the VM lock and validates the event: the user ID must match, the desired state must be running and sleepy, the observed state must be `sleeping`, and a snapshot generation must be recorded. A stale or duplicate event is a safe no-op, so a normal pass still owns every real state change. A valid event restores with the snapshot start mode, requires running, publishes running, and then disarms.

Failures never lose the guest. A restore failure keeps the VM `sleeping` and the snapshot, and a later hold pass rearms it, so a new packet notifies again. A restore that runs but does not record running is completed on the next pass from the running runtime, without loading the snapshot again. The trigger packet can be lost while no process has `tap0` open, so a client must retry, usually over TCP. An established TCP or vsock connection is not guaranteed to survive the restart.

One pass holds the VM lock for its whole duration, and every operation that changes desired state takes the same lock. A mutation therefore never lands halfway through a pass. A wake event uses the same lock, so it waits for a warm stop to finish and then restores.

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
