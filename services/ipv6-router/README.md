# Atlas IPv6 router

The IPv6 router gives VMs public IPv6 addresses from one block. A region can have many routers, each with its own block. It translates each packet between the public address and the WG Mesh address of the VM. It keeps no connection state and no VM list.

## Address layout

```text
public  = <block prefix> | reserved, all zero | tenant 24 bits | VM 20 bits
private = fdaa | region 16 bits | tenant 32 bits | VM 64 bits
```

The block must be a canonical subnet of the global-unicast range `2000::/3`. Its prefix length can be from `/3` to `/84`. The reserved field fills the space between the prefix and the 44 host bits.

Example for block `2001:db8:1:2:3::/80`:

| Mesh address | Public address |
| --- | --- |
| `fdaa:1:0:abcd::5` | `2001:db8:1:2:3:a:bcd0:5` |

A VM with a tenant ID of 2^24 or more, or a VM number of 2^20 or more, has no public address.

## Packet path

```text
Internet -> Metal host -> router eth0 -> tc ingress: destination block -> mesh -> WG Mesh -> VM
VM -> gateway route 2000::/3 -> router eth0 -> tc ingress: source mesh -> block -> Metal host -> Internet
```

The eBPF program on `eth0` ingress changes the address and corrects the L4 checksum. For an ICMPv6 error, it also changes the address in the quoted packet. The kernel forwards the packet out of `eth0`.

The program drops these packets:

- A packet to a block address with a reserved bit set.
- A packet from a mesh address that has no public address, to the Internet.
- A translated packet that is not TCP, UDP, ICMPv6, or an IPv6 fragment of one of these protocols.

## Code

| Path | Content |
| --- | --- |
| `bpf/address.h` | Address layouts and the mapping. |
| `bpf/packet.h` | Offsets, checksum location, and address writes. |
| `bpf/router.c` | The tc program. |
| `nftables/router.nft` | Forward filter and redirect block. |
| `systemd/atlas-ipv6-router.service` | Default route, nft table, and tc attachment. |
| `setup.sh` | Installation on Ubuntu 24.04. |

## Setup

Atlas installs the router. See [IPv6 Router Server](../../atlas/service/doctype/ipv6_router_server/SPEC.md).

To install by hand, run this command as root on a tenant-0 gateway VM that owns the block:

```sh
REGION_ID=1 PUBLIC_IPV6_PREFIX=2001:db8:1:2:3::/80 ./setup.sh
```

`setup.sh` installs Clang, libbpf, iproute2, nftables, and the kernel modules for the running kernel. It loads `sch_ingress`, `cls_bpf`, and `nf_tables`. It writes `config.h` with the region and the block, and compiles the program on the VM. It enables IPv6 forwarding and starts `atlas-ipv6-router.service`. You can run it again.

## Checks

```sh
systemctl status atlas-ipv6-router.service
tc filter show dev eth0 ingress
nft list table ip6 atlas_ipv6_router
```

The nft counters show forwarded packets in each direction.

## Limits

| Limit | Reason |
| --- | --- |
| One router for each VM | A VM has one `2000::/3` gateway route. |
| One fragment header only | The program drops translated packets with another extension header because it cannot safely correct the L4 checksum. |
| The router itself has no public address | The program runs on ingress only. |
