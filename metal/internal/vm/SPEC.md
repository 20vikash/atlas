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

## Boundaries

`Runtime`, `Network`, `Storage`, and `Snapshots` are consumed here and implemented by host packages. The manager uses `traffic.Monitor` for samples and watch operations. The daemon traffic listener passes each `traffic.Event` to `Manager.RestoreAfterTraffic`.

`WarmImageBuilder` creates shared start artifacts. It does not use the saved state of an idle VM.

## Related

- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) implements `Runtime`.
- [internal/storage/SPEC.md](../storage/SPEC.md) implements `Storage` and `Snapshots`.
- [internal/network/SPEC.md](../network/SPEC.md) implements `Network`.
- [internal/network/traffic/SPEC.md](../network/traffic/SPEC.md) owns packet tracking.
- [internal/reconciler/SPEC.md](../reconciler/SPEC.md) drives reconciliation and traffic events.
