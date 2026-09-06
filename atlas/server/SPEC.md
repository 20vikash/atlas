# Server module specification

[Atlas app specification](../SPEC.md)

For the lifecycle overview, see [docs/server-lifecycle.md](../docs/server-lifecycle.md).

## Purpose

A Server is one Metal host. This module takes it from a provider API call to a machine that can run virtual machines, and then keeps Atlas informed about its capacity.

Provisioning is a sequence of phases, not one transaction. Each phase records progress, so a failed run resumes at the phase that failed instead of repeating work.

## Types

| Type | Owns |
|---|---|
| `Server` (DocType) | Lifecycle hooks, permissions, and the whitelisted API. |
| `provisioning` | The phase order, progress saves, and failure logs. |
| `host_installation` | Installing and configuring Metal on the host. |
| `disk_inventory` | Reading block devices into Server Disk rows. |
| `catalog_sync` | Refreshing Server Size and Server Image from the provider. |
| `ServerIPAddress` (DocType) | One public IPv4 address and its provider intent. |
| `ServerSSHTask` (DocType) | One recorded SSH command and its result. |
| `ServerUsage` (DocType) | One capacity sample reported by Metal. |

## Provisioning

```text
create provider server   one stable provider identity across retries
  -> wait for ready
  -> attach addresses
  -> install Metal
  -> configure WireGuard
  -> mark provisioning complete
```

Every phase is safe to repeat. A creation retry reuses the provider identity the first attempt made, so a lost response cannot produce a second machine. Compensation deletes only a provider server that the current request created.

## Capacity

`POST /v1/sync` sends the desired host state and returns capacity in the same exchange. Atlas records the result as a Server Usage row, which is what placement later reads.

A transport fault and a response fault are logged separately, because they need different responses: one is a network problem, the other is a host problem.

## Public IPv4

An address carries a desired intent and an intent version. Reconciliation applies the intent and preserves a pending one for a retry, so a failed apply is never mistaken for a completed one.

## Related

- [docs/server-lifecycle.md](../docs/server-lifecycle.md) describes the lifecycle and its reasons.
- [docs/providers.md](../docs/providers.md) describes the provider contract.
- [atlas/atlas/SPEC.md](../atlas/SPEC.md) describes provider implementations and settings.
