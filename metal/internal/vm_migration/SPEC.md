# vm_migration

The `vmmigration` package owns VM live migration on a Metal host. It owns the lifecycle, records, and host transport. The `VMMigration` struct is the public entry point.

## Ownership

`VMMigration` stores migration records beside VM records and runs one worker per VM. It uses the `Machines` interface, which `vm.Manager` implements. Tests can use a fake. See [Boundary](#boundary).

```text
machines/<vm-id>/config.json            the VM's own records (vm package)
machines/<vm-id>/status.json
machines/<vm-id>/migration/target.json  target reservation and state
machines/<vm-id>/migration/source.json  source lock
```

One VM ID locates all VM state. A source record blocks VM changes and reconciliation. A target record reserves the VM ID, hides the VM from list and get, and pauses reconciliation. A migration ID is different from a VM ID, so the target scans records to find the VM.

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

The target reserves compute only after the handshake supplies the config. One host allocation lock covers the capacity check and the saved reservation. Host capacity subtracts that reservation while the target stays out of the VM list. A target that stays in preparing for 10 minutes expires. The target then aborts the source and releases the reservation.

## Copying

During copying, `VMMigration` runs one disk transfer per VM and waits for transfers at shutdown. The reconciler starts or resumes it through `AdvanceTarget`; it does not move data. The transfer holds no VM lock while data moves and drives copying, stopping, and starting from the saved phase.

An HTTP request, the transfer worker, and the reconciler all change the target record. Every read-modify-write of that record holds the migration lock of its VM. A caller that already holds the lock writes through `writeTarget`. Every other caller uses `mutateTarget`, which takes the lock, reads the stored record again, applies the change, and writes it. The worker therefore cannot write a copy it read before a long operation and drop an abort that arrived in the meantime. The lock is held only for the read and the write, so an abort stays available while data moves. The lock is not reentrant, so a locked caller must not call `mutateTarget`. An interval completion and a transfer failure detach the context, because the snapshot or the reason is already real when the worker stops.

```text
next snapshot from source  (acknowledge the last completed sequence)
        |
        v
start the source snapshot listener
        |
        v
target connects -> zfs recv -s -> resume the same sequence after an interrupt
        |
        v
compare received GUID with the source GUID
        |
        v
mark the interval complete with the estimated stream size
        |
        v
copy or cut over
```

The source keeps the acknowledged snapshot as the next incremental base and removes the previous one. The target saves each interval before and after transfer. A record write error ends the transfer pass and reports the storage error. A stopped source needs one full interval. A network break or restart resumes the same sequence. A GUID mismatch, invalid sequence, or wrong target dataset fails the migration and keeps the data for inspection.

## Cutover

A running source always sends the first full interval while it runs. For each later delta the target compares its size with `migration.final_delta_mib`.

```text
delta larger than final_delta_mib -> copy it, limit the source disk (64, 32, 16, 8 MiB/s)
delta at or below final_delta_mib -> stop the source
16 intervals or 30 minutes        -> stop the source
non-running source                -> stop the source before the first transfer
```

The target sends the temporary limit on the stream header. The source applies the lower of that limit and the configured disk limit, and never raises an existing limit.

The stop request acknowledges the last completed interval. The source creates the final snapshot after the highest acknowledged sequence.

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

The source is stopped before the final snapshot. A running VM stops. A paused VM resumes, then stops. A created VM stops without guest work. A stopped VM with saved state restores, then stops. Saved memory is not transferred. The target cold-starts the disk and keeps the record at `ready` until Atlas commits.

The source stop, network removal, final snapshot, target network, and target state are checkpointed before the next external operation. A restart repeats only safe work. A failure after the source stops marks the migration failed and keeps both hosts locked with snapshots for rollback.

## Completion and recovery

One cancellable background worker per VM drives finish and abort as well as the transfer. Atlas records a request on the target, which wakes the worker.

```text
finish (Atlas, target ready) -> destroy source -> remove received snapshots -> completed
abort  (Atlas, nonterminal)  -> cancel transfer -> rollback -> aborted
```

A finish and an abort are mutually exclusive: the first request wins, and the other returns a conflict. A finish is valid only after the target reports ready.

```text
abort, source not stopped -> clean target -> unlock the still-available source
abort, source stopped     -> clean target -> restore the source -> unlock the source
```

Rollback removes the target runtime, network, dataset, and staging records. It aborts a partial receive before removing the dataset. It creates the target network only after the source network is gone. A stopped source is restored with a cold start, then unlocked. A rollback failure keeps both records, locks, and the source disk, and sets phase `rollback`.

The source stops any active disk stream before it unlocks or destroys the VM. This releases the migration snapshot before unlock removes it.

A source lock that the target stopped using for 15 minutes releases itself. The reconciler expires it only when the source never stopped, no stream is in flight, and no source control call arrived in that window. An age alone is not enough, because one long interval makes no control call. The release runs the same cleanup as an unlock and leaves the record as a tombstone with `expired`, which frees the VM but refuses the same migration ID, so a target that comes back cannot resume against a VM the operator has since changed. A source that already stopped is never released here: the target may have started the guest, and two hosts must never own one disk.

A nonterminal target that Atlas stops calling for 15 minutes rolls itself back. It records the time of each Atlas control call, and Atlas gives a target up after its own 10 minute visibility timeout, so nothing else would ever release the reservation, the dataset, and the hidden VM ID on that host. A `ready` target is never released this way, because Atlas may already point the VM at it.

A success or abort keeps a terminal record with the schema version, migration ID, VM ID, status, and finish time. It has no phase, hides no VM, and reserves no capacity. Every nonterminal record, including failed, hides the VM and reserves capacity. A new target migration can replace an aborted record only after all VM, source-lock, and target-dataset records are gone.

The daemon removes and logs a migration record that it cannot decode. One corrupt or old record cannot block startup.

## Transport

Metal hosts use WireGuard addresses and certificates from the same regional authority. The target sends the VM ID as `virtual_machine_id`, so the source finds the migration record.

Control calls use HTTPS and mutual TLS on port 9001. They prepare the source, select snapshots, start a stream, stop or restore the source, and remove it. A normal control call has a 60 second timeout. The stop call has a 120 second timeout because it can restore and shut down a guest.

Snapshot bytes use port 9002 and do not pass through an HTTP body. The source binds a `crypto/tls` listener, accepts the one target connection, closes the listener, and relays the `zfs send` pipe into the connection. The relay is one direction only: the target never writes on that connection. The control request returns after the listener is bound, so a bind failure is reported to the target instead of a connection that is refused later.

```text
source: zfs send -> pipe -> tls.Listen  (one accept, then closed)
                                  |
                                  | TLS 1.3 and node certificates
                                  v
target: zfs recv <- pipe <- tls.Dial
```

The transport must stay one direction. A two-way relay on this connection deadlocks, because the target sends nothing and any read of the connection waits forever while the send pipe fills.

The source permits one outbound snapshot stream at a time because all source streams use the fixed port. A second stream request returns a conflict. This limit does not affect Atlas API calls or migration work on target hosts.

The source stream is not bound to its control request, so it ends at the daemon root context, at an unlock, or at the 30 minute transfer limit. A target that never connects releases the port after 2 minutes, because the accept wait is bounded: otherwise it would hold the fixed port, and make the source lock look busy, until the transfer limit ran out.

## Boundary

`VMMigration` calls the VM manager only through `Machines`. The interface reads and writes VM records, allocates IDs, takes locks, releases storage, and runs migration runtime and network operations. `vm.Manager` implements it.

The `vm` package never imports this package. `vm.Manager` reads lock state through the injected `vm.MigrationGuard`. `VMMigration` implements the guard, and the daemon injects it with `(*vm.Manager).SetMigrationGuard`.

## Related

- [internal/vm/SPEC.md](../vm/SPEC.md) owns the VM records, state transitions, and the migration runtime and network operations this package drives.
- [internal/api/SPEC.md](../api/SPEC.md) owns the control routes.
- [cmd/metald/SPEC.md](../../cmd/metald/SPEC.md) wires the manager, the migration, the guard, and the transfer listener.
