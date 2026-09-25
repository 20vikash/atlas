# How traffic reaches a VM

Traffic takes a different path depending on where it comes from. Metal sets up each VM's network on the host. WG Mesh carries private, proxy, and routed IPv6 traffic. A direct public IP reaches the guest through its assigned host. Read [how VMs reach each other](index.md) first.

## The four traffic paths

| Traffic | Path |
| --- | --- |
| Private VM traffic | Guest namespace → WG Mesh → destination namespace. |
| Public site traffic | HTTP proxy VM → WG Mesh → guest. |
| Direct public IP | Assigned host network → guest namespace. |
| Routed public IPv6 | IPv6 router VM → WG Mesh → guest. |

`metald` configures the network but is not in the packet path.

## Private traffic

WG Mesh finds the destination host and checks tenant access. The VM keeps its private address when it moves. The [network basics](index.md#the-location-problem) explain discovery and the [Metal host guide](host-networking.md#connect-a-guest-to-the-host) explains the guest links. A new location lookup can drop the first packet.

## Public web traffic

OpenResty routes requests using local maps. The proxy control daemon stores and replicates those maps.

- **Atlas** provisions proxy VMs, DNS, certificates, credentials, and membership. It writes its own service routes.
- **Central** writes tenant site and custom-domain routes through the proxy API.

## Public IP traffic

A direct allocation saves the host and VM network changes. A routed IPv6 allocation derives an address from a router block and uses the router's mesh address as the VM gateway.

The IPv6 router translates address bits without a per-VM lookup table. Metal applies routes, NAT where needed, firewalls, and private and public throughput policers.

## Failure and recovery

| Failed path | Check in order |
| --- | --- |
| Private | Metal namespace, WireGuard peers, WG Mesh location. |
| Site HTTP or HTTPS | DNS, proxy readiness, route generation, mesh reachability. |
| Direct public address | Saved allocation request and assigned host route. |
| Routed IPv6 | Router VM, public block, VM gateway route. |

After sync or migration, peers can briefly retain an old location.

**Details:** [Metal networking](host-networking.md), [WG Mesh](wg-mesh/index.md), [HTTP proxy](http-proxy/index.md), and [IPv6 router](ipv6-router.md).

::: details Source code and tests

- [Mesh address derivation](../../atlas/atlas/core/mesh_address.py) defines stable VM addresses.
- [Metal network allocator](../../metal/internal/network/linux_allocator.go) builds the namespace and host links.
- [Public address service](../../atlas/metal_server/core/public_ip_service.py) owns direct and routed allocation requests.
- [Mesh VM hook](../../services/wg-mesh/bpf/vm.h) and [mesh map checks](../../services/wg-mesh/bpf/maps.h) route private packets.
- [Proxy data plane](http-proxy/openresty.md) describes public HTTP paths.
- [Router address code](../../services/ipv6-router/bpf/address.h) translates public IPv6 and mesh addresses.

:::
