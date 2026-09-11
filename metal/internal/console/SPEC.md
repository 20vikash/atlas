# console: VM serial consoles

For Go code, follow the repository [Go anti-pattern rules](../../../llm/go-code-review-guide.md).

[internal SPEC](../SPEC.md) · overview: [docs/api.md](../../docs/api.md)

## Purpose

A virtual machine writes serial output whether or not a person is watching. The `console` package accepts that output at all times, keeps the recent part, and replays it to each viewer that attaches later.

Output must never block the guest. A viewer that cannot keep up is disconnected instead.

This package owns the serial console only. The API serves an SSH session on the same endpoint, but that mode goes to `vm.Manager.ConnectSSH` and does not use this package.

## Types

| Type | Responsibility |
|---|---|
| `SerialBroker` | Owns the console of every running VM and maps a VM ID to it. |
| `DescriptorStore` | Keeps PTY masters open across a restart of metald. |
| `console` | Owns one PTY master, its scrollback, and its viewers. |
| `ringBuffer` | Holds the most recent output for replay. |
| `Winsize` | Carries a viewer terminal size to the PTY. |

## PTY model

Each VM gets one PTY pair. Metal keeps the master. The systemd unit opens the slave through a stable symlink, so the unit needs no knowledge of the allocated PTY name.

```text
Firecracker --writes--> PTY slave <--symlink-- <sockets>/consoles/<id>
                            |
                        PTY master  (held by console)
                            |            \
                     drain goroutine      systemd descriptor store
                       |          |
                  ringBuffer   viewers --> API WebSocket
```

`Open` allocates the master and starts the drain. `Persist` stores it. `Close` releases it. `Attach` adds a viewer. `Shutdown` releases local masters. `Adopt` restores stored masters.

Linux destroys a PTY when its master closes. The slave is the controlling terminal of Firecracker. A closed master therefore sends SIGHUP to the guest process and stops the VM.

The systemd descriptor store holds the masters until the VM stops. systemd keeps its copy when it passes a master to the next metald process, so one console survives many restarts.

A master is non-blocking. A release stops the drain read at once. One drain at a time reads a PTY.

The unit needs `NotifyAccess`, `FileDescriptorStoreMax`, and `FileDescriptorStorePreserve=yes`. The first two enable the store. The third keeps it when metald stops.

systemd watches each stored descriptor. It closes a descriptor that reports `EPOLLHUP`. A PTY master reports `EPOLLHUP` while no slave is open.

`Open` keeps the master out of the store. `Persist` adds it after the unit holds the slave.

## Backpressure

The drain goroutine is the single reader of the master. It copies each chunk into the scrollback and offers it to every viewer. An offer that would block is skipped and that viewer is dropped and disconnected. This keeps one stalled WebSocket from stopping the guest or the other viewers.

A viewer that attaches receives the scrollback first, then live output. Scrollback is bounded, so a viewer that attaches late sees recent output, not the whole boot.

## Lifecycle

```text
metald start               -> Adopt(running VM IDs)
firecracker prepareLaunch  -> Open(id)
firecracker prepareLaunch  -> Persist(id)   after the unit start
api  GET /v1/vms/{id}/console?mode=tty -> Attach(id)   many, concurrent, capped
firecracker stop or remove -> Close(id)
metald shutdown            -> Shutdown()
```

`Adopt` restores consoles for running VMs and removes stale masters and links.

A console outlives its viewers. It exists from VM launch to VM stop, so output produced while nobody is attached still reaches the next viewer. An SSH session has the opposite lifetime: it starts and ends with one WebSocket, and it replays nothing.

Linux reports EIO on a PTY master while no slave is open. That is the normal state between `Open` and the unit start, so the drain treats EIO as idle until the console is closed.

## Related

- [docs/api.md](../../docs/api.md) documents the console WebSocket endpoint.
- [internal/api/SPEC.md](../api/SPEC.md) owns the WebSocket framing and resize messages.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) opens and closes a console with the VM.
