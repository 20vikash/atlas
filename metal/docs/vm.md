# VM functionality

[metal SPEC](../SPEC.md) · contract: [internal/vm/SPEC.md](../internal/vm/SPEC.md)

A virtual machine has persisted metadata, desired state, and observed host state. The controller requests changes, and Metal reconciles them.

## Lifecycle

```text
reservation -> unknown -> running <-> paused
                           |
                           v
                         stopped

any retained state -> desired destroyed -> cleanup -> removed
runtime error -> failed
```

Create reserves the supplied VM ID and requests `running`. It does not wait for Firecracker to start. Every mutation returns before the host is touched, so a caller polls until the desired and observed generations match.

The image snapshot API only stages a VM disk for upload. It does not restore a VM or promote an image in place.

## Cold boot vs warm start

Cold boot clones an image disk, links the kernel, configures Firecracker, and starts the guest. Warm boot restores host-local disk, memory, and Firecracker state. Warm boot is an optimization: any failure falls back to cold boot.

A plain start uses the shared warm image for the VM's exact image and shape, or a cold boot. A start of a sleeping VM restores that VM's own memory snapshot. So the resume is deliberate, and a VM that stops outside Metal cold boots.

## Warm stop and the sleeping state

A warm stop and automatic sleep share one observed state. The `sleeping` state means no Firecracker process, a valid memory snapshot, and a start that resumes. The desired record says why the VM sleeps.

A power request may carry `warm`, valid only with a `stopped` state: `PUT /v1/vms/{id}/power` with the body `{"state":"stopped","warm":true}`. A warm stop pauses the guest, saves a memory snapshot, terminates the process, and reaches the `sleeping` state. A later start resumes the guest instead of cold booting. A VM that stops outside Metal, such as a guest power off or a crash, reaches `stopped` and cold boots on the next start. A plain stop, a restart, or a specification change resumes the guest first and then applies the change, which discards the snapshot. A resume failure keeps the VM `sleeping` and never cold boots, so the in-memory guest is not lost. The snapshot stays on the host and is never uploaded.

Records, generations, reconciliation, and cleanup: [internal/vm/SPEC.md](../internal/vm/SPEC.md). Boot and stop mechanics: [internal/firecracker/SPEC.md](../internal/firecracker/SPEC.md).

## Sleepy VMs

A VM specification has an `is_sleepy` flag. When it is set, and the host turns on automatic sleep, Metal can sleep the VM after an idle timeout. The create request and the sleep-policy endpoint carry only `is_sleepy`. The idle timeout is one Metal-wide host value and is never part of a VM request or record. See [testing.md](testing.md) for the `[sleep]` host configuration.

`sleeping` is an observed state only. The controller cannot request it. When a VM is asleep, the observed response reports `state` as `sleeping`, and reports `last_network_activity_at` and `sleeping_since` when they are known. The response never carries a snapshot path or a timeout.

When automatic sleep is on, Metal warm-stops an idle sleepy VM to `sleeping` after the idle timeout, and the desired state stays running. A later pass holds the VM asleep. A packet during the warm stop aborts the sleep and keeps the VM running. A controller change that needs a running guest resumes it from the snapshot. A sleeping VM also wakes on a host-to-guest TCP packet: Metal restores the memory snapshot and reports running. The first packet can be lost, so a client must retry, usually over TCP. Automatic sleep is off by default.

## Design notes

- The controller supplies the VM ID, so reservation retries use one stable resource.
- Desired state keeps API requests fast and lets reconciliation retry host operations.
- Observed state is separate because process changes are asynchronous.
- The stopped state keeps the disk for a new start. The destroyed state removes all owned resources.
- Compute and disk changes use separate endpoints because their safety rules differ.
- Warm boot is an optimization. Cold boot remains the reliable path.
