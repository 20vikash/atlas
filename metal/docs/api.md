# Metal HTTP API

Metal serves JSON on its configured listener. Metal generates the OpenAPI document from handler annotations.

Open `/docs` for the interactive API page. Get the generated document from `/docs/swagger.json`.

The [Metal `/v1` contract](../../docs/metal-v1-contract.md) contains request and response examples.

## Authentication

The health and documentation routes are public. All `/v1` routes need a bearer token.

```http
Authorization: Bearer <token>
```

Set `metald.auth_token_hash` to the lowercase SHA-256 digest of the token.

## Routes

The controller API uses `/v1`. Metal does not provide unversioned controller routes.

| Method | Path | Success | Action |
|---|---|---:|---|
| `GET` | `/health` | `200` | Check the HTTP server |
| `GET` | `/docs` | `200` | Open the API page |
| `GET` | `/docs/swagger.json` | `200` | Get the OpenAPI document |
| `POST` | `/v1/sync` | `200` | Replace controller-owned host sets |
| `PUT` | `/v1/vms/{id}` | `202` | Create or confirm a VM reservation |
| `GET` | `/v1/vms` | `200` | List VMs |
| `GET` | `/v1/vms/{id}` | `200` | Get one VM |
| `PUT` | `/v1/vms/{id}/power` | `202` | Set the power state |
| `POST` | `/v1/vms/{id}/restart` | `202` | Request a restart |
| `PUT` | `/v1/vms/{id}/compute` | `202` | Set CPU and memory values |
| `PUT` | `/v1/vms/{id}/disk` | `202` | Set disk size and rate limits |
| `PUT` | `/v1/vms/{id}/network` | `202` | Set the complete network |
| `PUT` | `/v1/vms/{id}/ssh-keys` | `200` or `202` | Replace all SSH keys |
| `PUT` | `/v1/vms/{id}/metadata` | `200` or `202` | Replace all metadata |
| `DELETE` | `/v1/vms/{id}` | `202` | Request VM removal |
| `POST` | `/v1/vms/{id}/snapshots` | `201` | Create image staging |
| `POST` | `/v1/snapshots/{id}/upload` | `202` | Start an artifact upload |
| `GET` | `/v1/snapshots/{id}` | `200` | Get upload status |
| `DELETE` | `/v1/snapshots/{id}` | `204` | Remove image staging |
| `GET` | `/v1/vms/{id}/console?mode=tty` | `101` | Attach to the shared serial console |
| `GET` | `/v1/vms/{id}/console?mode=ssh` | `101` | Open a private SSH session |

## Console modes

One endpoint serves 2 modes. `mode=tty` is the default.

| Mode | Session | History | Viewers |
|---|---|---|---|
| `tty` | The VM's serial console. It runs from VM start to VM stop. | Recent output replays on attach. | Many, capped. A viewer that stops reading is disconnected. |
| `ssh` | An SSH session created for this connection and closed with it. | None. | One. |

Both modes use the same framing. Binary frames carry terminal bytes in both directions. A text frame carries a control message, for example `{"resize":{"cols":120,"rows":40}}`. Full detail: [internal/console/SPEC.md](../internal/console/SPEC.md).

## Virtual machine state

Each VM response has 2 state objects:

- `desired` contains the stored controller intent.
- `observed` contains the latest host result.

Mutation responses contain the same resource shape as a get response. Poll the resource until both generation values match.

Metal does not return signed URLs, user data, host paths, process IDs, or host user IDs.

## Mutations

Power, restart, compute, disk, network, and delete requests store intent first. Metal returns `202` and applies the intent through reconciliation.

The compute request replaces the CPU and memory values. The VM must be stopped before this request.

The disk request replaces its size and both rate limits. Metal rejects a size that is smaller than the current size.

The network request replaces all network values. Send the current value for each setting that does not change.

SSH key and metadata requests use a 2-second immediate operation. Metal returns `200` if this operation succeeds.

Metal returns `202` if reconciliation must complete the SSH key or metadata request.

## Snapshots

Snapshot creation stages the root file system and kernel on the host. The response contains the snapshot ID and exact artifact sizes.

The upload route accepts consecutive multipart URLs. Metal does not return these URLs.

Poll the snapshot until its state is `completed` or `failed`. A completed response contains part numbers, ETags, sizes, and SHA-256 values.

## Synchronization

`POST /v1/sync` replaces 3 complete sets:

- WireGuard peers.
- Cached image policies.
- Privileged VM addresses.

Each field is mandatory. Send an empty array to remove all values in a set.

The response contains the current CPU, memory, storage, and VM capacity values.

## Errors

Every HTTP error contains a stable code, a safe message, a retryable value, and a request ID.

```json
{
  "error": {
    "code": "not_found",
    "message": "resource not found",
    "retryable": false,
    "request_id": "request-123"
  }
}
```

Use the request ID to find the matching JSON log entry. Metal does not put a host command error in the response.
