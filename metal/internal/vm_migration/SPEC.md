# vm_migration

The `vmmigration` package carries VM migration traffic between Metal hosts. It owns the target-to-source transport and nothing about migration policy, which stays in [internal/vm](../vm/SPEC.md).

## Ownership

The target host drives every migration. It makes the control calls and pulls the disk. This package holds both sides of that wire:

- `SourceClient` runs the target-side calls. It implements the `vm.MigrationSourceClient` interface that the migration manager consumes.
- `SourceListener` runs on the source host. It accepts disk stream connections and serves each one from the local VM through the `StreamHandler` interface, which `vm.MigrationManager` satisfies.

The package imports `internal/vm` for the shared record and value types. It is never imported by `internal/vm`, so there is no import cycle. The daemon wires the client into the manager and starts the listener.

## Trust

Metal hosts reach each other only over the trusted WireGuard mesh, so migration traffic carries no credential. Atlas keeps the static token for its own control calls to Metal.

## Control calls

The control calls use HTTP against the source Metal API. Each call carries the VM ID as the `virtual_machine_id` query value, so the source resolves the migration without a token. The calls are prepare source, next snapshot, stop, start, destroy, and remove.

## Disk stream

The disk moves over a plain TCP connection to a fixed source `transfer_port`, not an HTTP body. Every host in a region uses the same port.

One connection carries one snapshot:

1. The target dials the source transfer port and writes a length-prefixed JSON header. The header names the migration, VM, sequence, resume token, and throughput limit.
2. The source replies with one status byte. `0` is followed by the raw `zfs send` stream. `1` is followed by a length-prefixed error message, used when the source rejects the request before any data.
3. The target runs `zfs recv` from the connection and counts the bytes.

The throughput limit applies a temporary combined read and write limit to the source VM disk for that interval. It never raises the configured limit. The limit is applied to the VM, not to the connection, so it is independent of the transport.

## Related

- [internal/vm/SPEC.md](../vm/SPEC.md) owns the migration state machine, records, and the source-side stream handler.
- [internal/api/SPEC.md](../api/SPEC.md) owns the control routes the client calls.
- [cmd/metald/SPEC.md](../../cmd/metald/SPEC.md) wires the client and starts the listener.
