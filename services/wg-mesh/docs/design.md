# Atlas WG Mesh design

This document describes the active packet paths and their state.

Read [Operations](operations.md) for the CLI commands.

## Addresses

| Range | Use |
| --- | --- |
| `fdaa::/16` | Virtual machine addresses. |
| `fdab::/16` | Host WireGuard addresses. |

The virtual machine address has this format:

```text
fdaa | region 16 bits | tenant 32 bits | virtual machine ID 64 bits
```

Example: `fdaa:0001:0000:0002:0000:0000:0000:0003` identifies region `1`, tenant `2`, and virtual machine `3`.

The data path uses the tenant field for isolation.

## BPF files

All hooks use one BPF object and one set of pinned maps.

| File | Purpose |
| --- | --- |
| `bpf/mesh.h` | Address and packet functions. |
| `bpf/maps.h` | Maps and map lookups. |
| `bpf/vm.h` | Ingress hook for a virtual machine interface. |
| `bpf/wireguard.h` | Ingress hook for the WireGuard interface. |
| `bpf/uplink.h` | Uplink, public interface, and unicast NDP hooks. |
| `bpf/bpf.c` | One BPF build entry point. |

## Maps

| Map | Value |
| --- | --- |
| `config` | Host interfaces, addresses, and MAC addresses. |
| `local_vms` | Virtual machine address to local interface. |
| `remote_vms` | Remote virtual machine address to host WireGuard address. |
| `privileged_vms` | Privileged tenant-0 addresses. |
| `peer_list` | Peer IPv4, MAC, and WireGuard addresses. |
| `discovery_limits` | NDP request limit for each virtual machine interface. |
| `local_gateway` | The gateway virtual machine on this host. |
| `owned_prefixes` | Public prefix to owner interface. |
| `gateway_routes` | Virtual machine and destination prefix to gateway address. |
| `build_hash` | Hash of the active BPF object. |

`remote_vms` uses least recently used eviction.

A location stays until eviction or a valid `NOT_HERE` packet removes it.

## Virtual machine traffic

The virtual machine hook applies these rules in order:

1. Drop traffic to `fdab::/16`.
2. Send a configured external destination to its gateway.
3. Check source ownership.
4. Check tenant access.
5. Leave local delivery to Linux.
6. Tunnel a known remote destination through WireGuard.
7. Start NDP for an unknown destination.

Each interface can start 10 NDP requests each second, with a burst of 50 requests.

## Discovery

Neighbor Discovery Protocol (NDP) finds the host of an unknown virtual machine.

```text
VM hook -> neighbor solicitation -> destination host proxy NDP
        <- neighbor advertisement <-
uplink hook -> remote_vms
```

The uplink hook maps the peer MAC address to its WireGuard address.

The virtual machine retries its packet after NDP completes.

In unicast mode, the egress hook puts each NDP packet in IPv4 protocol 41.

It sends one copy to each peer.

The receiving uplink hook checks the peer IPv4 address and removes the IPv4 header.

Linux NDP and proxy NDP work the same in both modes.

## WireGuard traffic

The WireGuard hook handles packets only between addresses in `fdab::/16`.

| Packet | Action |
| --- | --- |
| Tunnel to a local virtual machine | Remove the outer IPv6 header. |
| Tunnel to a missing virtual machine | Return `NOT_HERE` to the sender. |
| Reply for an external client | Send the packet to the local gateway. |
| Valid `NOT_HERE` | Remove the old location and start NDP. |

`NOT_HERE` uses IPv6 next header `253` and contains one virtual machine address.

Only the host stored in `remote_vms` can remove that location.

## Gateways

A route selects a gateway by the longest destination prefix.

One host can have one gateway because a reply does not identify the original local gateway.

The public interface hook answers NDP for an address in an owned prefix.

Read [Gateways](gateways.md) for the configuration model.
