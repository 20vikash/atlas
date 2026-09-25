# Metal

The host daemon, `metald`, one per host. It stores each VM's desired state and makes Firecracker, systemd, ZFS, and Linux networking match it.

- How it works: the [developer handbook](../docs/start/what-atlas-is.md)
- Codebase guide and dev setup: [Metal](../docs/develop/metal.md)
- Code contract and package map: [SPEC.md](SPEC.md)
- Files on a host: [host layout](../docs/storage/host-layout.md)

Run Go commands from `metal/`: `make test`, `make vet`, `make build`.
