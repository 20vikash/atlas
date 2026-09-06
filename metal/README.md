# Metal

Metal is the host daemon for Atlas virtual machines. The executable is `metald`.

Metal owns desired state, observed state, reconciliation, host resources, and cleanup progress. Atlas owns provider resources and user actions.

## Start here

1. [Architecture](docs/architecture.md) explains what Metal is and how the parts fit.
2. [Testing](docs/testing.md) brings up a development host and runs a VM.
3. [Development](docs/development.md) lists the checks to run on a change.

Package detail lives in each package's `SPEC.md`. Start at [`internal/SPEC.md`](internal/SPEC.md) for the package map.

The [Metal `/v1` contract](../docs/metal-v1-contract.md) defines the controller API.

## Main packages

| Package | Purpose |
|---|---|
| `cmd/metald` | Compose and start the daemon. |
| `internal/api` | Expose controller operations. |
| `internal/vm` | Define virtual machine domain behavior. |
| `internal/firecracker` | Control Firecracker and its systemd unit. |
| `internal/network` | Own Linux network and WireGuard peer operations. |
| `internal/storage` | Own ZFS images, disks, and snapshot staging. |
| `internal/reconciler` | Apply current desired state. |
| `internal/host` | Synchronize controller-owned state and report capacity. |
| `internal/console` | Serve VM serial consoles. |
| `internal/platform` | Host files, commands, and systemd. |

## Local checks

Run everything from `metal/`. The commands are in [development](docs/development.md). Host tests need Linux, root access, KVM, ZFS, systemd, iptables, and Atlas WG Mesh.
