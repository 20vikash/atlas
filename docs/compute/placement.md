# How Atlas chooses a host

Atlas chooses a host for a new VM, a resize, or a move. The code calls this **placement**. It must never let two requests reserve the same memory or disk.

A **strategy** ranks hosts. The **capacity check** decides whether a host can take the VM. A strategy can change the order, never the capacity rule.

## How a host is chosen

1. **Host pool.** A host qualifies when it is `Running` and has finished setup. It must also match the image architecture and have a capacity sample less than 2 minutes old. With **dedicated sleepy hosts** enabled, [sleepy VMs](sleepy-vms.md) and regular VMs use separate pools.
2. **Fit.** Atlas subtracts work the sample cannot include yet: recent drafts, VMs created after the sample, and incoming migrations. **Memory and disk are hard limits.** CPU can be oversubscribed. It only affects ranking.
3. **Rank.** The selected strategy sorts the hosts that fit.
4. **Lock and recheck.** Atlas takes a MariaDB named lock for the top host, rechecks committed capacity under `READ COMMITTED`, commits the draft, and releases the lock.

A resize or migration first tries to stay on the current host, which then needs room only for the increase.

## Strategies

Choose the strategy in **Atlas Settings**. `balanced` is the default.

| Strategy | Goal | Regular VMs | Sleepy VMs |
| --- | --- | --- | --- |
| `balanced` | Spread risk and load | Rank by the keys below | Same keys, with sleepy memory discounted |
| `spread-3` | Keep hosts evenly used | Least used host first | Pack: most subscribed host first |
| `best-fit` | Keep whole hosts free for large VMs | Most used host first | Pack: most subscribed host first |

**`balanced`** sorts hosts by these keys, in order:

1. Fewest VMs from the same tenant, so one host failure hits fewer of a tenant's VMs.
2. Lowest placement rate in the last 5 minutes, so a burst spreads out.
3. Smallest CPU shortfall: requested CPU above the host's free CPU.
4. Lowest projected memory or disk use, whichever is higher.
5. Host name, as a tie-breaker.

For example, if host A already runs 2 of the tenant's VMs and host B runs 1, host B wins even if A is quieter. For a sleepy VM, `balanced` divides sleepy memory by `sleepy_vm_overcommit_factor` when it ranks, because sleepy VMs are often stopped. The capacity check still uses real memory.

**`spread-3`** ranks regular hosts by projected use, lowest first. A VM that would bring host A to 80% and host B to 60% goes to host B. It packs sleepy VMs onto the host with the most subscribed sleepy memory, so sleepy hosts fill one at a time.

**`best-fit`** ranks regular hosts by projected use, highest first: the same VM goes to host A. This packs VMs tightly and keeps other hosts empty for large requests. It packs sleepy VMs the same way as `spread-3`.

To add a strategy, subclass `PlacementStrategy` and register it. The [VM module specification](../../atlas/vm/SPEC.md#placement) shows how.

## Under contention

Concurrent requests rank hosts the same way, so they would all wait on the same top host. If another request holds that host's lock, Atlas tries the remaining hosts in random order.

It probes at most 16 hosts, tries up to 3 times, and gives up after **0.4 seconds** in total.

| Result | Meaning | Action |
| --- | --- | --- |
| Host selected | The draft or migration holds the capacity. | Atlas sends the host request. |
| `out_of_capacity` | No host in the pool fits. | Retry later. With auto-spawn enabled, Atlas queues a new host. |
| `placement_busy` | Hosts fit, but all were locked until the deadline. | Retry after `Retry-After` (1 second). |

Lock contention never creates a host. Only a full pool does.

## Limits and recovery

A healthy host drops out of the pool when its sample gets older than 2 minutes, for example after failed host syncs. An uncertain create keeps its capacity reserved until Metal confirms presence or absence. Check sample age and draft reservations before you change the strategy.

## Experimental

Offline and live simulators compare strategies outside the request path. Their results do not prove a strategy is safe for a region.

::: details Source code and tests

- [Placement context](../../atlas/vm/core/placement/context.py) loads samples, subtracts reservations, and rechecks capacity under the host lock.
- [Placement strategy base](../../atlas/vm/core/placement/strategies/base.py) ranks candidate hosts and defines capacity and busy results.
- [Transaction setup](../../atlas/vm/core/placement/transaction.py) enables READ COMMITTED.
- [VM creation](../../atlas/vm/core/vm_service.py) commits the draft after placement.
- [Placement tests](../../atlas/vm/core/placement/test_context.py) check capacity and lock behavior.

:::
