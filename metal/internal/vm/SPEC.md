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

## Reconciliation

```text
desired running   -> Start or Resume
desired paused    -> Start when necessary, then Pause
desired stopped   -> Stop
desired destroyed -> Remove runtime -> Release network -> Release storage
```

One pass holds the VM lock for its whole duration, and every operation that changes desired state takes the same lock. A mutation therefore never lands halfway through a pass.

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
