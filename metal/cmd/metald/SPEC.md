# metald: the daemon entrypoint

For Go code, follow the repository [Go anti-pattern rules](../../../llm/go-code-review-guide.md).

[metal SPEC](../../SPEC.md) · overview: [docs/architecture.md](../../docs/architecture.md)

## Purpose

Command `metald` loads host configuration, creates runtime services, and starts the HTTP server. It is the composition root.

## Command

```text
metald serve [--config path]
```

The default configuration path is `/var/lib/metal/metald.toml`. A missing default file is permitted.

## Types

| Type | Role |
|---|---|
| `options` | Resolved Firecracker, ZFS, WireGuard, Atlas WG Mesh, traffic monitor, listener, authentication, and base-directory settings. |
| `fileConfig` | TOML sections for metald, Firecracker, Jailer, ZFS, WireGuard, Atlas WG Mesh, and traffic monitoring. |

## Startup wiring

```text
load configuration
   -> create required directories
   -> connect to systemd
   -> create storage stores
   -> create the WireGuard manager
   -> optionally connect Atlas WG Mesh and configure the host
   -> optionally create the traffic monitor
   -> create the Firecracker runtime
   -> validate all VM records and create the VM manager
   -> create the host service
   -> create the VM and image reconcilers
   -> start the traffic listener when monitoring is enabled
   -> create the authenticated API
   -> listen and serve
```

The storage constructor receives the daemon context, pool, image directory, and logger. It returns the pool, VM, image, and snapshot stores.

The network constructor receives optional mesh and traffic monitor services. The VM manager receives the runtime, network, storage, snapshot, optional traffic monitor, and logger services.

The host service receives an optional mesh service, WireGuard, image, VM, storage, and reconciler services. The API receives this service as one dependency.

`connectMesh` runs on every start when `wg_mesh.enabled` is true. Each VM reconciliation calls `Network.Ensure` to restore and update its network.

`wg_mesh.enabled = false` does not disable managed WireGuard peers.

`wg_mesh.uplink` has no default. The Atlas WG Mesh uplink hook consumes discovery traffic for every VLAN under the interface it attaches to, so a parent interface silently blackholes discovery for its own VLANs. Only the controller knows which interface carries discovery, so metald requires the name.

## Config keys

| Key | Default | Meaning |
|---|---|---|
| `metald.base_dir` | `/var/lib/metal` | Stores machines, images, policies, peers, and staging files. |
| `metald.listen` | `127.0.0.1:8080` | TCP address or `unix:/path`. |
| `metald.auth_token_hash` | none | Required lowercase SHA-256 token digest. |
| `firecracker.binary_path` | `/usr/bin/firecracker` | Firecracker binary. |
| `firecracker.sockets_dir` | `/run/metal` | Short API socket links. |
| `jailer.binary_path` | `/usr/bin/jailer` | Jailer binary. |
| `zfs.pool` | `metal` | ZFS pool name. |
| `wireguard.interface` | `wg0` | Underlay interface for managed peers and Atlas WG Mesh. |
| `wg_mesh.enabled` | `true` | Enables Atlas WG Mesh host setup and VM mesh registration. |
| `wg_mesh.binary_path` | `/usr/local/bin/atlas-wg-mesh` | Atlas WG Mesh CLI. Required. |
| `wg_mesh.uplink` | none | Discovery uplink. Required. |
| `traffic_monitor.enabled` | `true` | Enables VM packet monitoring and idle shutdown. |

See `config.example.toml` for the complete file format.

## Runtime loops

The VM reconciler processes desired VM states. The image reconciler downloads cached images, creates warm artifacts, and removes idle local data. When traffic monitoring is enabled, one traffic listener dispatches each restoration in a separate daemon-owned goroutine.

The daemon owns all reconciler goroutines and snapshot upload jobs. `SIGINT` or `SIGTERM` starts a bounded graceful shutdown. It stops accepting HTTP requests, cancels reconciliation, waits for workers, closes the enabled traffic monitor, closes console sessions, and closes the systemd connection. It does not stop or destroy guest virtual machines.

The daemon writes structured JSON logs. Log records include request and operation correlation fields when the operation comes from the API.

## Related

- [docs/architecture.md](../../docs/architecture.md) describes the runtime graph.
- [internal/api/SPEC.md](../../internal/api/SPEC.md) describes the server.
- [internal/firecracker/SPEC.md](../../internal/firecracker/SPEC.md) describes the runtime.
- [docs/testing.md](../../docs/testing.md) describes host setup.
