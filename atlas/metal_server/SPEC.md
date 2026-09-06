# Metal Server module specification

[Atlas app specification](../SPEC.md)

For the lifecycle overview, see [docs/metal-server-lifecycle.md](../docs/metal-server-lifecycle.md).

## Purpose

A Metal Server is a provider host where Metal runs virtual machines. This module creates it through a provider, installs Metal, and keeps its capacity current.

Provisioning is a sequence of phases, not one transaction. Each phase records progress, so a failed run resumes at the phase that failed instead of repeating work.

## Types

| Type | Owns |
|---|---|
| `MetalServer` (DocType) | Lifecycle hooks, permissions, and the whitelisted API. |
| `provisioning` | The phase order, progress saves, and failure logs. |
| `host_installation` | Installing and configuring Metal on the host. |
| `disk_inventory` | Reading block devices into Metal Server Disk rows. |
| `catalog_sync` | Refreshing Metal Server Size and Metal Server Image from the provider. |
| `MetalServerIPAddress` (DocType) | One public IPv4 address and its provider intent. |
| `MetalServerSSHTask` (DocType) | One recorded SSH command and its result. |
| `MetalServerUsage` (DocType) | One capacity sample reported by Metal. |

## Provisioning

```text
create provider host     one stable provider identity across retries
  -> wait for ready
  -> attach addresses
  -> install Metal
  -> configure WireGuard
  -> mark provisioning complete
```

Every phase is safe to repeat. A creation retry reuses the provider identity from the first attempt, so a lost response cannot create a second host. Compensation deletes only a host that the current request created.

## Capacity

`POST /v1/sync` sends the desired host state and returns capacity in the same exchange. Atlas records the result as a Metal Server Usage row, which is what placement later reads.

A transport fault and a response fault are logged separately, because they need different responses: one is a network problem, the other is a host problem.

## Public IPv4

An address carries a desired intent and an intent version. Reconciliation applies the intent and preserves a pending one for a retry, so a failed apply is never mistaken for a completed one.

## Related

- [docs/metal-server-lifecycle.md](../docs/metal-server-lifecycle.md) describes the lifecycle and its reasons.
- [docs/providers.md](../docs/providers.md) describes the provider contract.
- [atlas/atlas/SPEC.md](../atlas/SPEC.md) describes provider implementations and settings.
