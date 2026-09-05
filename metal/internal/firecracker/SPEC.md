# firecracker: VM runtime

[internal SPEC](../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

Package `firecracker` implements `vm.Runtime`. Systemd owns each jailed Firecracker process.

## Types

| Type | Role |
|---|---|
| `Runtime` | Controls Firecracker processes and guest operations. |
| `machine` | Holds the input and API client for one runtime operation. |
| `Config` | Contains machine paths, socket paths, and binary paths. |
| `MemorySnapshotBuilder` | Creates optional warm image artifacts. |

The runtime receives VM disk storage, image storage, systemd, and console services. It does not read or write VM records.

## Composition

```text
vm.Manager -> firecracker.Runtime -> systemd -> jailer -> Firecracker
                         |
                         +-> VM disk storage
                         +-> image storage
                         +-> console broker
```

## Launch

Cold and warm launch paths use the same preparation operation:

```text
write jailer.env and socket link
open the console
start the systemd unit
set resource limits
wait for the Firecracker socket
prepare cold or warm storage
configure or load the VM
```

The runtime selects a warm artifact by image identity, VM shape, and Firecracker compatibility. It uses a cold launch after a warm launch failure.

## State

`Inspect` combines the systemd state and the Firecracker state. An API fault returns `StateUnknown` and an error to the manager.

Stop sends Ctrl+Alt+Del and waits for 30 seconds. It sends `SIGKILL` if the guest does not stop.

`Remove` stops the systemd unit and removes runtime files. The manager releases the network and storage resources.

## Warm artifacts

`MemorySnapshotBuilder` asks the manager to run a temporary VM without egress. The manager owns its user ID, network, runtime, and disk cleanup.

The builder waits for 5 minutes and pauses the VM. It then stores the disk, Firecracker state, and memory as a local artifact.

## Related

- [internal/firecracker/api/SPEC.md](api/SPEC.md) describes the Firecracker client.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines the runtime interface.
- [internal/storage/SPEC.md](../storage/SPEC.md) describes boot storage and warm artifacts.
- [docs/vm.md](../../docs/vm.md) gives the VM lifecycle.
