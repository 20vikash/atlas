# vm_migration

The `vmmigration` package owns VM live migration on a Metal host: the lifecycle, the on-disk migration records, and the host-to-host transport. Another library drives a migration through the `VMMigration` struct.

## Ownership

`VMMigration` owns the migration records beside the VM records and one background worker per VM. It reaches the VM manager through the `Machines` interface, which `vm.Manager` satisfies, so the daemon passes the concrete manager while this package stays testable with a fake. See [Boundary](#boundary).

```text
machines/<vm-id>/config.json            the VM's own records (vm package)
machines/<vm-id>/status.json
machines/<vm-id>/migration/target.json  target reservation and state
machines/<vm-id>/migration/source.json  source lock
```

One VM ID locates all of a VM's state. A source record blocks every VM mutation and pauses reconciliation. A target record reserves the VM ID, hides the VM from the list and get, and pauses reconciliation. A migration ID differs from a VM ID, so the target resolves a migration ID to its VM with a small scan.

## Preparing

```text
Atlas PUT -> target record (preparing), reserve the VM ID
                 |
                 v
           lock the source and read its portable config (over the mesh)
                 |
                 v
           check capacity -> allocate local IDs -> reconstruct records
                 |
                 v
           phase copying
```

The target reserves compute only after the handshake supplies the config. Host capacity subtracts that reservation while the target stays out of the VM list. A target that never advances past preparing for 10 minutes is expired: the target aborts the source and releases the reservation.

## Copying

At the copying phase, `VMMigration` runs one background disk transfer per VM and waits for the transfers at shutdown. The reconciler starts or resumes the transfer through `AdvanceTarget` and never moves data itself. The transfer holds no VM lock while data moves. One background transfer drives the copying, stopping, and starting phases in order, so `AdvanceTarget` resumes any of them from the saved phase.

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
copy or cut over
```

The source keeps the acknowledged snapshot as the next incremental base and removes the one before it. A stopped source needs one full interval. A network break or a restart leaves the migration in copying and resumes the same sequence. A GUID mismatch, an invalid sequence, or an unrelated target dataset fails the migration and keeps the dataset and snapshots for inspection.

## Cutover

A running source always sends the first full interval while it runs. For each later delta the target compares its size with `migration.final_delta_mib`.

```text
delta larger than final_delta_mib -> copy it, limit the source disk (64, 32, 16, 8 MiB/s)
delta at or below final_delta_mib -> stop the source
16 intervals or 30 minutes        -> stop the source
non-running source                -> stop the source before the first transfer
```

The target sends the temporary limit on the stream header. The source applies the lower of that limit and the configured disk limit, and never raises an existing limit.

```text
stopping: /stop -> source stops, removes its network, takes the final snapshot
        |
        v
receive and verify the final snapshot
        |
        v
starting: create the target network -> apply the original desired state (cold start)
        |
        v
verify the target state -> status ready
```

The source normalizes to stopped before the final snapshot: a running VM stops, a paused VM resumes then stops, a created VM stops without guest work, and a stopped VM with saved state restores then stops. Saved memory is not transferred. The target cold-starts the received disk, so it never inherits the source guest memory. The target keeps the migration record at `ready`, so normal reconciliation stays blocked until Atlas commits.

Each source stop, network removal, final snapshot, target network, and target state is checkpointed before the next external operation, so a restart repeats only idempotent work. A failure after the source stop marks the migration failed and keeps both hosts locked and the snapshots preserved for rollback.

## Completion and recovery

One cancellable background worker per VM drives finish and abort as well as the transfer. Atlas records a request on the target, which wakes the worker.

```text
finish (Atlas static token, target ready) -> destroy source -> remove received snapshots -> completed
abort  (Atlas static token, nonterminal)  -> cancel transfer -> rollback -> aborted
```

A finish and an abort are mutually exclusive: the first request wins, and the other returns a conflict. A finish is valid only after the target reports ready.

```text
abort, source not stopped -> clean target -> unlock the still-available source
abort, source stopped     -> clean target -> restore the source -> unlock the source
```

The rollback removes the target runtime, network, dataset, and staging records. It aborts a partial receive before it removes the dataset. It creates the target network never before the source network is gone. A stopped source is restored to its original desired state with a cold start, then unlocked. A rollback failure keeps both records, both locks, and the source disk, and reports failed with phase rollback.

The source stops any in-flight disk stream before it unlocks or destroys the VM. A running `zfs send` therefore never keeps the migration snapshot busy, so the snapshot destroy during unlock always succeeds.

A success or abort keeps a compact terminal record with only the schema version, migration ID, VM ID, terminal status, and finished time. A terminal record has no phase, does not hide the VM, and reserves no capacity. Every nonterminal record, including failed, keeps the VM hidden and its capacity reserved. A new target migration can replace an aborted record only when no VM record, source lock, or target dataset remains.

A migration record that cannot be decoded, from a schema change or a corrupt file, is removed at startup and logged, so one leftover record never blocks the daemon.

## Transport

Metal hosts reach each other only over the trusted WireGuard mesh, so migration traffic carries no credential. The target drives every call and carries the VM ID as the `virtual_machine_id` query value, so the source resolves the migration without a token.

The control calls use HTTP against the source Metal API: prepare source, next snapshot, stop, start, destroy, and remove. The disk moves over a plain TCP connection to a fixed source `transfer_port`, not an HTTP body. Every host in a region uses the same port.

One connection carries one snapshot:

1. The target dials the source transfer port and writes a length-prefixed JSON header naming the migration, VM, sequence, resume token, and throughput limit.
2. The source replies with one status byte. `0` is followed by the raw `zfs send` stream. `1` is followed by a length-prefixed message, used when the source rejects the request before any data.
3. The target runs `zfs recv` from the connection and counts the bytes.

## Boundary

`VMMigration` calls the VM manager only through the `Machines` interface: read and write VM records, allocate IDs, take the per-VM and allocation locks, release storage, and run the migration runtime and network operations (stop the source, remove and create networks, apply state, cold start, limit and refresh the disk). The `vm.Manager` implements every method.

The reverse direction never imports this package. `vm.Manager` reads migration lock state through the `vm.MigrationGuard` it is given. `VMMigration` implements that guard from its own records, and the daemon injects it with `(*vm.Manager).SetMigrationGuard`.

## Related

- [internal/vm/SPEC.md](../vm/SPEC.md) owns the VM records, state transitions, and the migration runtime and network operations this package drives.
- [internal/api/SPEC.md](../api/SPEC.md) owns the control routes.
- [cmd/metald/SPEC.md](../../cmd/metald/SPEC.md) wires the manager, the migration, the guard, and the transfer listener.
