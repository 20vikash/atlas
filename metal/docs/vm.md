# VM functionality

[metal SPEC](../SPEC.md) · contract: [internal/vm/SPEC.md](../internal/vm/SPEC.md)

A virtual machine has persisted metadata, desired state, and observed host state. The controller requests state changes, and Metal reconciles them.

## Lifecycle

```text
reservation -> unknown -> running <-> paused
                           |
                           v
                         stopped

any retained state -> desired destroyed -> cleanup -> removed
runtime error -> failed
```

Create reserves the supplied VM ID and sets the desired state to `running`. It does not wait for Firecracker to start.

Power, restart, compute, disk, network, and delete requests return `202`. Poll until the desired and observed generations match.

## Operations

| Operation | Effect |
|---|---|
| Create | Store a VM reservation and request `running`. |
| Set power | Request `running`, `stopped`, or `paused`. |
| Delete | Request cleanup of all VM resources. |
| Set compute | Replace CPU and memory while stopped, then request `running`. |
| Set disk | Replace the size and rate limits. The size cannot decrease. |
| Set network | Replace the complete desired network. |
| SSH key replacement | Replace all keys and refresh MMDS when active. |
| Snapshot | Create rootfs and kernel image staging. |
| Restart | Increase the restart generation. Keep the request after daemon restart. |

Metal does not support in-place snapshot restore or promotion.

## Cold boot vs warm start

Cold boot clones an image disk, links the kernel, configures Firecracker, and starts the guest.

Warm boot loads host-local disk, state, and memory artifacts for an exact image and VM shape. If warm boot fails, Metal uses cold boot.

`vm.WarmImageBuilder` coordinates temporary VM work. Firecracker supplies memory snapshot operations, and storage owns the artifact files.

## Stop escalation

Metal asks the guest to power off and kills it when it does not answer within a timeout: [internal/firecracker/SPEC.md](../internal/firecracker/SPEC.md).

## Cleanup

Delete stores the desired `destroyed` state. Cleanup then runs as part of reconciliation and can resume after an interruption: [internal/vm/SPEC.md](../internal/vm/SPEC.md).

## Records

Each VM keeps a desired record and an observed record on disk. Record contents, generation rules, and startup validation: [internal/vm/SPEC.md](../internal/vm/SPEC.md).

## Design notes

- The controller supplies the VM ID, so reservation retries use one stable resource.
- Desired state keeps API requests fast and lets reconciliation retry host operations.
- Observed state remains separate because process changes are asynchronous.
- The stopped state keeps the disk for a new start. The destroyed state removes all owned resources.
- Compute and disk changes use separate endpoints because their safety rules differ.
- Warm boot is an optimization. Cold boot remains the reliable path.
