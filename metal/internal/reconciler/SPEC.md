# reconciler: convergence loops

[internal SPEC](../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

The controller records what a host should hold. It does not command the host step by step. The `reconciler` package closes that gap: it reads the desired state, compares it to the host, and repeats until they agree.

Every pass is a full sweep, not a queue of events. A missed wake, a failed operation, or a restart costs time, not correctness.

## Types

| Type | Responsibility |
|---|---|
| `VirtualMachineReconciler` | Drives every VM towards its desired record. |
| `ImageReconciler` | Caches the images the controller selects and prunes the rest. |
| `NetworkWakeReconciler` | Restores a sleeping VM after a packet wake event. |
| `passScheduler` | Runs one pass on an interval and on demand. Both sweep reconcilers embed it. |
| `VirtualMachineManager`, `ImageStore`, `SnapshotStore`, `MemorySnapshotBuilder`, `NetworkWakeManager` | The work each pass calls out to. |

## Pass model

```text
Run(ctx)
  |
  +-> pass ---------------------------+
  |                                   |
  +-- wait: interval tick             |
           wake request               |
           ctx canceled -> return     |
                                      |
  VM pass:     ListIDs -> N workers -> Reconcile(id)
  image pass:  ImagePolicies -> EnsureImage -> EnsureMemorySnapshot
                             -> PruneImages -> PruneStagedSnapshots
```

A pass runs first, then the loop waits. A new reconciler therefore converges at startup without waiting one interval.

`Wake` never blocks. The wake channel holds one request, so many wakes that arrive during a pass collapse into one pass after it. The API calls `Wake` after a mutation, so a change is applied at once instead of at the next tick.

## Network wake

`NetworkWakeReconciler` handles one event at a time:

```text
network monitor -> shared wake channel -> Manager.WakeFromNetwork -> restore
                                          |
                                          +-> one event at a time
                                          +-> operation timeout
                                          +-> failure: log and continue
```

It does not retry a failed event. A normal VM pass rearms a sleeping VM, so a later packet can send a new event. A canceled context or closed channel stops the reconciler.

## Bounds

Each operation runs under its own timeout, so one stuck VM or one slow download cannot stall the pass. The VM pass also bounds how many operations run at once.

Errors are logged, never returned: the next pass retries. Errors are suppressed while the context is canceled, because shutdown fails every operation still in flight and those failures say nothing about the host.

## Ownership

A reconciler owns its loop and nothing else. It holds no VM, image, or snapshot state, and it makes no decisions about desired state. `metald` owns the goroutine that calls `Run` and the context that stops it.

## Related

- [docs/architecture.md](../../docs/architecture.md) places the loops in the daemon.
- [internal/vm/SPEC.md](../vm/SPEC.md) owns VM reconciliation itself.
- [internal/storage/SPEC.md](../storage/SPEC.md) owns image caching and pruning.
