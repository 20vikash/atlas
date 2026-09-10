# HTTP API

[metal SPEC](../SPEC.md) · detail: [internal/api/SPEC.md](../internal/api/SPEC.md)

`metald` serves the controller API. The complete reference, with every field and status, is the OpenAPI document the binary embeds: open `GET /docs` in a browser, or read `GET /docs/swagger.json`.

## Model

A mutation stores desired state and returns. It does not wait for the host. The controller polls `GET /v1/vms/{id}` until the desired and observed generations match, and reads `observed.error` when they stop moving.

Each VM response nests `desired` and `observed`, so one read shows both what was asked for and what the host reached.

`PUT /v1/vms/{id}/power` sets the power state with `{"state": "running"|"stopped"|"paused"}`. A `stopped` request is always a hard stop and removes saved VM state.

`PUT /v1/vms/{id}/compute` sets `virtual_cpu_count`, `memory_mib`, and `sleep_after_idle_seconds`. A shape change needs a stopped VM. An idle timeout change is accepted in any state. `0` disables automatic idle shutdown. The response reports the value in `desired.compute`.

Every `/v1` route needs a bearer token. Controller routes take the static Metal token. A route that one Metal host calls on another takes an Atlas-signed token with the scopes the route names. Only liveness and the documentation are public.

Routes, status codes, and error mapping: [internal/api/SPEC.md](../internal/api/SPEC.md).

## Console modes

One endpoint serves two modes. `mode=tty` is the default.

| Mode | Session | History | Viewers |
|---|---|---|---|
| `tty` | The VM's serial console. It runs from VM start to VM stop. | Recent output replays on attach. | Many, capped. A viewer that stops reading is disconnected. |
| `ssh` | An SSH session created for this connection and closed with it. | None. | One. |

Both modes use the same framing. Binary frames carry terminal bytes in both directions. A text frame carries a control message, for example `{"resize":{"cols":120,"rows":40}}`. Full detail: [internal/console/SPEC.md](../internal/console/SPEC.md).

## Design notes

- Mutations are asynchronous, so a slow host never holds a controller connection open.
- Generations are the only completion signal, so every real change raises one.
- A response omits transport URLs, user data, and host paths: the controller cannot use them and must not store them.
