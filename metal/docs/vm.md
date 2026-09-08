# VM functionality

[metal SPEC](../SPEC.md) · contract: [internal/vm/SPEC.md](../internal/vm/SPEC.md)

A virtual machine has persisted metadata, desired state, and observed host state. The controller requests changes, and Metal reconciles them.

## Lifecycle

```text
reservation -> unknown -> running <-> paused
                            |  \
                  plain stop|   \ warm stop or idle timeout
                            v    v
                         stopped sleeping
                                   |
                                   +-> restore -> running

any retained state -> desired destroyed -> cleanup -> removed
runtime error -> failed
```

Create reserves the supplied VM ID and requests `running`. It does not wait for Firecracker to start. Every mutation returns before the host is touched, so a caller polls until the desired and observed generations match.

The image snapshot API only stages a VM disk for upload. It does not restore a VM or promote an image in place.

## Cold boot vs warm start

Cold boot clones an image disk and configures a new Firecracker guest. A normal start can use a shared warm image when the image and VM shape match. If that fails, it cold boots.

A sleeping VM uses its own memory snapshot. A failed restore keeps the VM sleeping. It never causes a cold boot that could lose guest memory.

## Warm stop and the sleeping state

A warm stop and automatic sleep use the observed `sleeping` state. It means that Firecracker is stopped and a valid memory snapshot exists.

```text
running -> pause -> save snapshot -> stop Firecracker -> sleeping
sleeping -> load snapshot -> update metadata -> resume -> running
```

To request a warm stop, send `{"state":"stopped","warm":true}` to `PUT /v1/vms/{id}/power`. A normal stop reaches `stopped`. A guest power off or crash also reaches `stopped` and uses a normal start next time.

Sleep snapshots stay on the host. Metal discards them for a normal stop, restart, removal, or incompatible specification change.

Records, generations, reconciliation, and cleanup: [internal/vm/SPEC.md](../internal/vm/SPEC.md). Boot and stop mechanics: [internal/firecracker/SPEC.md](../internal/firecracker/SPEC.md).

## Sleepy VMs

A VM can sleep after the host idle timeout when `is_sleepy` and `[sleep].enabled` are true. The timeout is one host value. It is not part of a VM request or record. See [testing.md](testing.md) for the configuration.

```text
running -> idle -> arm wake -> warm stop -> sleeping
   ^                                      |
   +------------- TCP wake ---------------+
```

The desired state stays `running` during automatic sleep. Traffic during the warm stop cancels sleep. A host-to-guest TCP packet wakes a sleeping VM. The first packet can be lost, so clients must retry.

`sleeping` is an observed state only. The response can include `last_network_activity_at` and `sleeping_since`. It does not include the host timeout or snapshot path. Automatic sleep is off by default.

## Design notes

- The controller supplies the VM ID, so reservation retries use one stable resource.
- Desired state keeps API requests fast and lets reconciliation retry host operations.
- Observed state is separate because process changes are asynchronous.
- The stopped state keeps the disk for a new start. The destroyed state removes all owned resources.
- Compute and disk changes use separate endpoints because their safety rules differ.
- Warm boot is an optimization. Cold boot remains the reliable path.
