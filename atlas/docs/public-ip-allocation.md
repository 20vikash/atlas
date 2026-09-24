# Public IP allocation

## Purpose

Atlas stores public address capacity in a Public IP Pool. A Public IP Allocation is one prefix that Atlas can give to a tenant or a virtual machine.

## Pool modes

| Mode | Pool owner | Allocation size | Allocation creation |
|---|---|---|---|
| Static direct | Operator | Configured length | Before use or on demand |
| Provider direct | AWS or Scaleway | Complete provider resource | On demand |
| Routed IPv6 | Operator or provider | `/128` | On demand |

A direct pool has no gateway. A routed pool has one IPv6 Router Server as its gateway.

## Static direct pools

The operator supplies the address range and makes it reachable on each eligible Metal Server. Atlas does not change the external route.

IPv4 pools always create `/32` allocations. IPv6 pools create the configured allocation length, such as `/128`.

Atlas checks enabled static direct pools each minute. If 200 or fewer allocations are available, Atlas creates up to 1,000 more.

An allocation request can also create the next allocation while it holds the pool lock. Atlas stops when the pool has no unused prefix.

## Provider direct pools

One provider pool represents one provider resource. Atlas does not divide a direct provider pool.

AWS supplies one `/32` Elastic IP or one `/80` IPv6 prefix. Scaleway supplies one Flexible IPv4 address or one `/64` IPv6 prefix.

A direct IPv6 provider pool gives its complete prefix to one virtual machine. The pool can serve only one virtual machine at a time.

Atlas creates the allocation record when a tenant reserves or attaches the resource. Background allocation creation does not process provider pools.

## Routed IPv6 pools

An IPv6 Router Server receives the complete pool. Atlas changes the pool allocation length to `/128` and gives each tenant virtual machine one address.

The router derives each public address from the private mesh address. This method gives a stable address without a stored inventory.

A routed pool must have a prefix length of `/84` or less. AWS `/80` and Scaleway `/64` prefixes satisfy this rule.

Routed allocations cannot be reserved. Atlas deletes a routed allocation after detach.

## Automatic IPv6

The **Route Automatic IPv6** setting controls only an automatic IPv6 request.

| Setting | Automatic selection |
|---|---|
| Disabled | An enabled direct IPv6 pool |
| Enabled | An enabled pool with an active IPv6 Router Server |

Atlas does not fall back to a direct pool when routed selection fails. A tenant can still attach a reserved direct allocation by ID.

## Provider assumptions

| Provider | Assumption |
|---|---|
| Generic | The operator routes each static direct pool to all eligible Metal Servers. Atlas makes no provider network change. |
| AWS IPv4 | AWS maps an Elastic IP to a secondary private address on the host interface. |
| AWS IPv6 | AWS delegates a `/80` to one host interface. Direct mode gives it to one VM. Routed mode gives it to the router VM. |
| Scaleway | Scaleway attaches one Flexible IP resource to a server. Direct mode gives the resource to one VM. Routed mode gives it to the router VM. |

Atlas detaches a provider resource before it moves the resource to a different Metal Server. Atlas releases the remote resource when an operator deletes its pool.

## Validation

Atlas applies these rules:

- Pool prefixes cannot overlap.
- An allocation must stay inside its pool.
- Allocation prefixes in one pool cannot overlap.
- A direct provider pool cannot use a smaller allocation length.
- Only an IPv6 pool can have a gateway.
- A gateway pool must create `/128` allocations.
- A pool cannot change its geometry while allocations exist.
- A routed pool must have no allocations before router creation.
- Allocation and virtual machine tenants must match.
- One virtual machine can have one allocation of each IP version.

Pool and allocation locks protect allocation creation and assignment from concurrent requests.

## Allocation lifecycle

An unreserved direct allocation returns to `Available` after detach. A reserved allocation returns to `Reserved` and keeps its tenant.

Release removes the reservation and returns the allocation to `Available`. A routed allocation is removed after detach.
