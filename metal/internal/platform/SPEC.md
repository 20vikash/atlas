# platform: host integration

[internal SPEC](../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

Package `platform` is Metal's boundary to host files, commands, and systemd. It keeps host integration details out of domain packages.

Metal talks to systemd through D-Bus. It does not call the `systemctl` command.

## Types

| Type | Responsibility |
|---|---|
| `UnitManager` | Defines the systemd operations needed by a VM runtime. |
| `DBus` | Implements `UnitManager` and owns the system-bus connection. |
| `Status`, `Result`, and `Limits` | Carry systemd state across the platform boundary. |

## Host commands

`Run` is for commands where the caller only needs success or failure. `Output` is for commands where the caller needs stdout. Both preserve command diagnostics in errors and accept a context that can stop a running command.

## Systemd units

One template unit runs each virtual machine:

```text
template: metal-vm@.service
instance: metal-vm@<id>.service

Metal --D-Bus--> systemd --> jailer --> Firecracker
```

`Connect` opens one system-bus connection and `Close` releases it. The runtime receives `UnitManager`, so it does not depend on D-Bus details.

| Operation | Behavior |
|---|---|
| Start and stop | Submit the requested transition and wait for systemd to finish it. A replace operation makes the newest request win. |
| Kill | Sends a signal to the unit's processes. |
| Reset failed | Clears a failed unit. An absent unit is ignored. |
| Status and list | Report units in terms the VM runtime can use. |
| Wait | Waits for a unit to stop and reports an exit code or signal. |
| Set limits | Applies the requested runtime resource limits. |

The context cancels systemd waits and polling. The VM runtime maps the returned unit state to VM state; that mapping belongs in the `firecracker` package.

## Related

- [docs/vm.md](../../docs/vm.md) describes the VM state machine.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) describes the unit's `ExecStart` and runtime state mapping.
- [docs/host-layout.md](../../docs/host-layout.md) lists host unit files and paths.
