# vm: virtual machine manager

[internal SPEC](../SPEC.md) · overview: [docs/vm.md](../../docs/vm.md)

## Purpose

This package owns VM records, state transitions, and reconciliation. Host packages implement runtime, network, storage, and snapshot operations through small interfaces declared here. The manager uses `traffic.Monitor` directly.

## Request flow

```text
API request -> desired record -> response
                    |
                    v
             reconcile pass -> Runtime, Network, Storage, traffic.Monitor
                    |
                    v
              observed record
```

The controller polls until observed generations match desired generations. The desired generation rises only for a real desired change. The restart generation rises for each accepted restart.

## Records

```text
machines/<id>/config.json   desired state and specification
machines/<id>/status.json   observed state and operation progress
```

The create fingerprint identifies one reservation request. It includes `sleep_after_idle_seconds` and excludes signed image URLs.

`sleep_after_idle_seconds` is compute configuration. `0` disables automatic idle shutdown. A timeout-only update does not change `SpecificationGeneration` because it does not change the Firecracker machine shape.

The daemon validates every record at startup. It does not repair or remove an invalid record.

## Reconciliation

```text
desired running   -> start, resume, restore saved state, or stop after idle
desired paused    -> start or restore when necessary, then pause
desired stopped   -> hard stop and delete saved state
desired destroyed -> remove runtime, network, storage, and records
```

The manager checks traffic only after normal running reconciliation completes. It starts event observation before the final sample. A sample error before state creation keeps the VM running. New traffic or a sample error after state creation restores the VM.

An automatically stopped VM keeps desired state `running` and reports observed state `stopped`. New traffic enters the same per-VM lock, validates the target and runtime, and restores the saved state. A stale or duplicate event has no effect.

A positive timeout can change while the VM stays stopped. A change to `0` restores it. A restart or machine shape change deletes the saved state and uses a normal start.

A valid saved state is authoritative when Firecracker is stopped. Restore failure keeps the saved state. Invalid state is an operation failure. A saved state beside a running Firecracker process is stale and is deleted.

The phase and operation ID are written before each host call. A failure stores a safe public message and local detail. Destroy records progress per resource so an interrupted destroy resumes safely.

## Migration

`MigrationManager` owns the migration records beside the VM manager. A host is the source or the target of one migration. The VM manager reads these records to lock a source and to hide a target, so it needs no reference to the migration manager.

```text
machines/<vm-id>/config.json            the VM's own records
machines/<vm-id>/status.json
machines/<vm-id>/migration/target.json  target reservation and state
machines/<vm-id>/migration/source.json  source lock
machines/<vm-id>/migration/token        the target-to-source token, mode 0600
```

One VM ID locates all of a VM's state. A source record blocks every VM mutation and pauses reconciliation. A target record reserves the VM ID, hides the VM from the list and get, and pauses reconciliation. A migration ID differs from a VM ID, so the target resolves a migration ID to its VM with a small scan.

```text
Atlas PUT -> target record (preparing), reserve the VM ID
                 |
                 v
           lock the source and read its portable config
                 |
                 v
           check capacity -> allocate local IDs -> reconstruct records
                 |
                 v
           phase copying   (a later sub-feature transfers the data)
```

The target reserves compute only after the handshake supplies the config. Host capacity subtracts that reservation while the target stays out of the VM list. A target that never advances past preparing for 10 minutes is expired: the target aborts the source and releases the reservation.

An abort before transfer removes the target staging, unlocks the source, and removes the migration records. A cleanup failure keeps the records and locks for a retry.

At the copying phase, `MigrationManager` runs one background disk transfer per VM and waits for the transfers at shutdown. The reconciler starts or resumes the transfer through `AdvanceTarget` and never moves data itself. The transfer holds no VM lock while data moves. One background transfer drives the copying, stopping, and starting phases in order, so `AdvanceTarget` resumes any of them from the saved phase.

```text
next snapshot from source  (acknowledge the last completed sequence)
        |
        v
stream into zfs recv -s -> resume the same sequence after an interrupt
        |
        v
compare received GUID with the source GUID
        |
        v
mark the interval complete, checkpoint bytes
        |
        v
copy or cut over  (see the cutover decision)
```

The source keeps the acknowledged snapshot as the next incremental base and removes the one before it. A stopped source needs one full interval. A network break or a restart leaves the migration in copying and resumes the same sequence. A GUID mismatch, an invalid sequence or token, or an unrelated target dataset fails the migration and keeps the dataset and snapshots for inspection.

### Cutover

A running source always sends the first full interval while it runs. For each later delta the target compares its size with `migration.final_delta_mib`.

```text
delta larger than final_delta_mib -> copy it, limit the source disk (64, 32, 16, 8 MiB/s)
delta at or below final_delta_mib -> stop the source
16 intervals or 30 minutes        -> stop the source
non-running source                -> stop the source before the first transfer
```

The target sends the temporary limit on the stream request. The source applies the lower of that limit and the configured disk limit, and never raises an existing limit.

```text
stopping: POST /stop -> source stops, removes its network, takes the final snapshot
        |
        v
receive and verify the final snapshot (the sub-feature 4 path)
        |
        v
starting: create the target network -> apply the original desired state (cold start)
        |
        v
verify the target state -> status ready
```

The source normalizes to stopped before the final snapshot: a running VM stops, a paused VM resumes then stops, a created VM stops without guest work, and a stopped VM with saved state restores then stops. Saved memory is not transferred. The target cold-starts the received disk, so it never inherits the source guest memory. The target keeps the migration record in place at `ready`, so normal reconciliation stays blocked until Atlas commits.

Each source stop, network removal, final snapshot, target network, and target state is checkpointed before the next external operation, so a restart repeats only idempotent work. A failure after the source stop marks the migration failed and keeps both hosts locked and the snapshots preserved for rollback.

## Boundaries

`Runtime`, `Network`, `Storage`, and `Snapshots` are consumed here and implemented by host packages. The manager uses `traffic.Monitor` for samples and watch operations. The daemon traffic listener passes each `traffic.Event` to `Manager.RestoreAfterTraffic`.

`WarmImageBuilder` creates shared start artifacts. It does not use the saved state of an idle VM.

## Related

- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) implements `Runtime`.
- [internal/storage/SPEC.md](../storage/SPEC.md) implements `Storage` and `Snapshots`.
- [internal/network/SPEC.md](../network/SPEC.md) implements `Network`.
- [internal/network/traffic/SPEC.md](../network/traffic/SPEC.md) owns packet tracking.
- [internal/reconciler/SPEC.md](../reconciler/SPEC.md) drives reconciliation and traffic events.
