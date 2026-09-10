# VM functionality

For Go code, follow the repository [Go anti-pattern rules](../../llm/go-code-review-guide.md).

[metal SPEC](../SPEC.md) · contract: [internal/vm/SPEC.md](../internal/vm/SPEC.md)

A virtual machine has persisted desired state and observed host state. The controller requests changes, and Metal reconciles them.

## Lifecycle

```text
reservation -> unknown -> running <-> paused
                            |
                            +-> stopped

any retained state -> desired destroyed -> cleanup -> removed
runtime error -> failed
```

Create reserves the supplied VM ID and requests `running`. Every mutation returns before host work completes. A caller polls until the desired and observed generations match.

A user power request supports `running`, `paused`, and `stopped`. A request for `stopped` always stops the VM without preserving guest memory.

## Starts and saved state

A normal start can use a shared warm image when the image and VM shape match. If no matching warm image exists, Metal uses a cold boot.

Automatic idle shutdown can preserve the memory of one VM. Metal keeps the public desired state as `running` and reports the observed state as `stopped` while Firecracker is not running.

```text
running -> idle -> save state -> stopped
   ^                              |
   +-------- new IP traffic ------+
```

Set `sleep_after_idle_seconds` in the compute settings. `0` disables automatic idle shutdown. Metal restores an automatically stopped VM after new host-to-guest IPv4 or IPv6 traffic. The first packet can be lost during restoration, so clients must retry.

Metal stores one saved state under `machines/<id>/saved-state/`. A restart, machine shape change, explicit stop, or VM removal deletes it. A restore failure keeps it for a later retry.

## Design notes

- The controller supplies the VM ID, so reservation retries use one stable resource.
- Desired state keeps API requests fast and lets reconciliation retry host operations.
- Observed state is separate because process changes are asynchronous.
- Compute and disk changes use separate endpoints because their safety rules differ.
- Warm images are a start optimization. VM saved state preserves one running guest.
