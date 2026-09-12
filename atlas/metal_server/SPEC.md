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
| `IPAddressService` | Tenant reservation, shared-pool claims, and release. |
| `MetalServerUsage` (DocType) | One capacity sample reported by Metal. |

The `Metal Server` module uses [SSH Task](../atlas/doctype/ssh_task/README.md) for host commands. The DocType belongs to the Atlas module.

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

A scheduled job queues one exchange for each ready server. The `sync_state` action queues one exchange for a single server, so an operator does not wait for the next scheduled run.

A transport fault and a response fault are logged separately, because they need different responses: one is a network problem, the other is a host problem.

The same exchange carries the Atlas public keys that the host must trust. Each host receives its own name as the receiver. A host that is inside the key overlap window receives the current key and the key it replaced. Atlas logs a missing signing key and syncs the host state without keys, so a capacity report never stops for it.

## Public IPv4

An address carries a desired intent and an intent version. Reconciliation applies the intent and preserves a pending one for a retry, so a failed apply is never mistaken for a completed one.

An address also carries the tenant that reserved it. An address without a tenant is in the shared pool. `IPAddressService` owns reservation and release. A reservation uses the shared pool or the provider. A pool claim locks one unowned row before it writes the tenant, so two requests never take the same address, and an empty pool is an error instead of a silent provider reservation. A release clears the tenant, keeps the provider reservation, and refuses an attached or detaching address.

A virtual machine can attach an address that its own tenant holds, or an unowned address from the shared pool. Attaching an unowned address claims it for the tenant of the virtual machine. An address that another tenant holds is refused.

Reset Tenant returns one unattached address to the shared pool from the desk. It needs the System Manager role, and it refuses an address that is already unowned. The action adds a comment that records the losing tenant, because the address keeps no trace of its previous owner.

## Related

- [docs/metal-server-lifecycle.md](../docs/metal-server-lifecycle.md) describes the lifecycle and its reasons.
- [docs/providers.md](../docs/providers.md) describes the provider contract.
- [atlas/atlas/SPEC.md](../atlas/SPEC.md) describes provider implementations and settings.
