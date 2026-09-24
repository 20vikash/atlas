# Metal `/v1` controller contract

This reference defines the released Atlas-to-Metal contract. The release replaced all unversioned controller routes in one change.

The health and documentation routes stay unversioned. All controller operations use `/v1`.

## Common rules

Metal accepts and returns JSON. Every route except the health and documentation routes needs a bearer token.

Requests use complete field names. Sizes use `mib`. Rates use `mibps`. IOPS values use `iops`.

Metal rejects unknown JSON fields, invalid values, and trailing JSON data. A mutation persists its intent before Metal sends a response.

Metal responses do not contain these values:

- Signed URLs.
- User data.
- Command output.
- Host paths.
- Process IDs.
- Host user IDs.

## Routes and status values

| Method | Path | Success status |
|---|---|---|
| `GET` | `/health` | `200` |
| `GET` | `/docs` | `200` |
| `GET` | `/docs/swagger.json` | `200` |
| `POST` | `/v1/sync` | `200` |
| `PUT` | `/v1/vms/{id}` | `202` |
| `GET` | `/v1/vms` | `200` |
| `GET` | `/v1/vms/{id}` | `200` |
| `PUT` | `/v1/vms/{id}/power` | `202` |
| `POST` | `/v1/vms/{id}/restart` | `202` |
| `PUT` | `/v1/vms/{id}/compute` | `202` |
| `PUT` | `/v1/vms/{id}/disk` | `202` |
| `PUT` | `/v1/vms/{id}/network` | `202` |
| `PUT` | `/v1/vms/{id}/ssh-keys` | `200` or `202` |
| `PUT` | `/v1/vms/{id}/metadata` | `200` or `202` |
| `DELETE` | `/v1/vms/{id}` | `202` |
| `POST` | `/v1/vms/{id}/snapshots` | `201` |
| `POST` | `/v1/snapshots/{id}/upload` | `202` |
| `GET` | `/v1/snapshots/{id}` | `200` |
| `DELETE` | `/v1/snapshots/{id}` | `204` |
| `GET` | `/v1/vms/{id}/console` | WebSocket upgrade |

Secure Shell key and metadata updates use an immediate operation with a 2-second limit. Metal returns `200` after immediate success. Metal returns `202` when reconciliation must continue.

## Create request

`PUT /v1/vms/{id}` reserves a caller-supplied ID. The first request fingerprint makes retries safe.

```json
{
  "compute": {
    "cpu_millicores": 1500,
    "memory_mib": 2048,
    "sleep_after_idle_seconds": 1800
  },
  "disk": {
    "size_mib": 20480,
    "throughput_mibps": 50,
    "iops": 2000
  },
  "image": {
    "ref": "sha256:immutable-reference",
    "architecture": "amd64",
    "rootfs": {
      "url": "signed transport URL",
      "sha256": "64 hexadecimal characters"
    },
    "kernel": {
      "url": "signed transport URL",
      "sha256": "64 hexadecimal characters"
    },
    "cache_image": true,
    "memory_snapshot": false
  },
  "network": {
    "routes": [{"destination": "0.0.0.0/0", "via": "host"}],
    "public_ipv4": "203.0.113.10",
    "wireguard_mesh_ipv6": "fdaa:1:1::1",
    "private_network_throughput_mibps": 100,
    "public_network_throughput_mibps": 50,
    "firewall": {"enabled": false, "inbound": [], "outbound": []}
  },
  "guest": {
    "hostname": "worker-1",
    "ssh_keys": ["ssh-ed25519 AAAA... user@example"],
    "metadata": {"role": "worker"},
    "user_data": "transport-only create data"
  }
}
```

A retry with the same fingerprint returns the current resource. A request with a different first-request identity returns `409`.

## Virtual machine response

Create, read, and mutation routes return the same nested resource shape.

```json
{
  "id": "vm-00001",
  "desired": {
    "generation": 4,
    "restart_generation": 1,
    "state": "running",
    "compute": {
      "cpu_millicores": 1500,
      "memory_mib": 2048,
      "sleep_after_idle_seconds": 1800
    },
    "disk": {
      "size_mib": 20480,
      "throughput_mibps": 50,
      "iops": 2000
    },
    "image": {
      "ref": "sha256:immutable-reference",
      "architecture": "amd64",
      "rootfs": {"sha256": "64 hexadecimal characters"},
      "kernel": {"sha256": "64 hexadecimal characters"},
      "cache_image": true,
      "memory_snapshot": false
    },
    "network": {
      "routes": [{"destination": "0.0.0.0/0", "via": "host"}],
      "public_ipv4": "203.0.113.10",
      "wireguard_mesh_ipv6": "fdaa:1:1::1",
      "private_network_throughput_mibps": 100,
      "public_network_throughput_mibps": 50,
      "firewall": {
        "enabled": true,
        "inbound": [{"protocol": "tcp", "ports": "22", "cidrs": ["203.0.113.0/24"]}],
        "outbound": [{"protocol": "any", "cidrs": ["0.0.0.0/0", "::/0"]}]
      }
    },
    "guest": {
      "hostname": "worker-1",
      "ssh_keys": ["ssh-ed25519 AAAA... user@example"],
      "metadata": {"role": "worker"}
    }
  },
  "observed": {
    "generation": 3,
    "restart_generation": 1,
    "state": "running",
    "phase": "network",
    "operation_id": "01900000-0000-7000-8000-000000000001",
    "operation_started_at": "2026-09-05T10:00:00Z",
    "updated_at": "2026-09-05T10:00:02Z",
    "disk": {"used_mib": 4096},
    "network": {"mac": "06:00:00:00:00:01"},
    "error": null
  }
}
```

The list route returns an array of these resources. Atlas compares the desired and observed generations to show progress.

The observed network object contains host-derived values. The desired network object contains controller intent.

## Mutation requests

Power replaces the desired lifecycle state:

```json
{"state": "stopped"}
```

Valid states are `running`, `stopped`, and `paused`. Delete records the desired destroyed state.

Restart has no request body. Each accepted request increases `restart_generation`.

Compute replaces the CPU entitlement, memory shape, and idle shutdown timeout. `cpu_millicores` accepts values from 100 through 32000. `1000` millicores equals one CPU core. The lower limit prevents impractical VM CPU quotas. The upper limit follows Firecracker's maximum of 32 guest vCPUs. Metal rounds the value up to select the guest-vCPU count and applies the exact value as the systemd CPU quota. A CPU or memory change needs a stopped VM. A timeout change does not. `sleep_after_idle_seconds` `0` disables automatic idle shutdown.

```json
{"cpu_millicores": 4000, "memory_mib": 4096, "sleep_after_idle_seconds": 1800}
```

Disk replaces the complete mutable disk object. Metal rejects disk shrink requests. A compute or disk increase that the host cannot hold returns `409` with the code `insufficient_capacity`.

Resize replaces CPU, memory, disk, and idle shutdown as one shape. Metal checks capacity before it writes the shape.

```json
{"size_mib": 40960, "throughput_mibps": 100, "iops": 4000}
```

Network replaces the complete network object:

```json
{
  "public_ipv4": "",
  "wireguard_mesh_ipv6": "fdaa:1:1::1",
  "routes": [{"destination": "2000::/3", "via": "fdaa:1::49"}],
  "is_network_gateway": false,
  "public_ipv6": "",
  "private_network_throughput_mibps": 100,
  "public_network_throughput_mibps": 0,
  "firewall": {
    "enabled": true,
    "inbound": [{"protocol": "tcp", "ports": "22", "cidrs": ["203.0.113.0/24"]}],
    "outbound": [{"protocol": "any", "cidrs": ["0.0.0.0/0", "::/0"]}]
  }
}
```

`routes` sends each destination range through the host uplink (`via` is `host`) or through the mesh address of a gateway VM. Only the host carries IPv4. The longest prefix wins, and a VM without routes reaches only the mesh. A public address needs a route via `host`. `is_network_gateway` lets the VM send a source address it does not own. `public_ipv6` is the IPv6 block that the host routes into the VM. These fields are empty when omitted.

The firewall supports `any`, `tcp`, `udp`, and `icmp`. An empty `ports` value selects all ports. Other port values select one port or one inclusive range from 1 through 65535.

Each rule needs one or more canonical IPv4 or IPv6 prefixes. One firewall can have at most 50 prefix entries across both directions. An enabled firewall blocks unmatched new traffic. A disabled firewall permits traffic and keeps its rules.

Secure Shell keys and metadata also use complete replacement.

```json
{"ssh_keys": ["ssh-ed25519 AAAA... user@example"]}
```

```json
{"metadata": {"role": "worker"}}
```

## Snapshot requests

Snapshot creation has no request body. It returns the staging identity and exact artifact sizes.

```json
{
  "id": "01900000-0000-7000-8000-000000000001",
  "rootfs": {"size_bytes": 21474836480},
  "kernel": {"size_bytes": 33554432}
}
```

The upload request supplies consecutive multipart URLs and the multipart `upload_id` each artifact belongs to. Metal does not return these URLs. Metal stores the parts it has sent against that upload ID, so an upload that restarts sends only the parts the object store does not hold, and a replaced multipart upload starts again.

The part count is an upper bound. Metal stores each artifact compressed with zstd, so it uses as many of the signed parts as the compressed artifact needs and leaves the rest unused. Atlas signs one spare part beyond the uncompressed size.

The snapshot status reports `pending`, `uploading`, `completing`, `completed`, or `failed`. A completed response contains part numbers, ETags, SHA-256 values, `size_bytes`, and `stored_size_bytes`. `size_bytes` is the uncompressed image, which is the size a VM disk must hold. `stored_size_bytes` is what the object store holds. The SHA-256 covers the uncompressed image, so one image keeps one digest whether it is stored raw or compressed.

A consumer detects the format from the content rather than the object name. A zstd artifact begins with the frame magic `28 B5 2F FD` and a raw artifact does not, so Atlas can serve either form.

## Synchronization

`POST /v1/sync` replaces the complete WireGuard peer, cached image, and privileged address sets. Empty arrays remove all managed values. `unicast` selects the unicast NDP transport of Atlas WG Mesh.

Each peer needs `public_address` as an IPv4 address and `private_network_mac_address` as the MAC of its private network interface. `private_address` is optional. An invalid peer returns `400`, because the mesh cannot learn a VM location from it.

The response contains current host capacity. CPU capacity uses `total_cpu_millicores` and `available_cpu_millicores`. Memory and storage use binary units. The virtual machine count uses a complete field name. CPU capacity is for ranking and visibility. It does not limit admission.

The response contains `private_network_mac_address`, the MAC of the host mesh uplink. It is absent when Atlas WG Mesh is disabled.

The response also contains `virtual_machines`. It maps each VM identifier on the host to an object with its last observed `status`. Metal reads the stored observed record of each VM, so the status is as fresh as the last reconcile pass.

The request carries an optional `jwt` object with the Atlas keys that sign the tokens one Metal host presents to another:

```json
{
  "jwt": {
    "issuer": "atlas-1",
    "receiver": "node-fra-00001",
    "public_keys": [{"id": "<key-id>", "key": "<base64url-public-key>"}]
  }
}
```

A sync without the object keeps the keys the host already holds. A repeated object is safe, and two keys let Atlas rotate one without a gap. Invalid key data returns `400`. The issuer and the receiver are fixed after the first successful sync, and a change returns `409`. A failed key update keeps the stored keys.

## Errors

Every HTTP error uses one safe object:

```json
{
  "error": {
    "code": "conflict",
    "message": "the virtual machine specification conflicts with the first request",
    "retryable": false,
    "request_id": "request-123"
  }
}
```

| HTTP status | Code | Meaning |
|---|---|---|
| `400` | `invalid_request` | The request syntax or value is invalid. |
| `401` | `unauthorized` | Authentication failed. |
| `403` | `forbidden` | A valid Atlas token does not allow the request. |
| `404` | `not_found` | The resource does not exist. |
| `409` | `conflict` | Current state or immutable identity blocks the request. |
| `409` | `insufficient_capacity` | A compute or disk increase does not fit on this host. |
| `409` | `image_content_conflict` | An image reference identifies different content. |
| `422` | `image_integrity_failed` | Downloaded image data failed verification. |
| `500` | `internal_error` | Metal failed and did not expose a host error. |
| `501` | `not_implemented` | The runtime does not implement the operation. |

The `request_id` identifies the matching Metal log entry. Atlas keeps the HTTP status, code, and retryable value at its Frappe boundary.

A transport failure after a write can also mark an Atlas request as uncertain.
