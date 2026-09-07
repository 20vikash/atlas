# Networking

[metal SPEC](../SPEC.md) · detail: [internal/network/SPEC.md](../internal/network/SPEC.md)

Every virtual machine gets the same private addresses. That is safe because each VM owns its own Linux network namespace, so nothing has to be allocated per VM and nothing has to be remembered between restarts.

## Topology

```text
guest eth0
    |
   tap0  172.16.0.1/24
    |
network namespace metal-<id>
    |
    +- egress none:   no veth pair, no path out
    +- egress mesh:   vg-<user-id> <-> vh-<user-id>
    +- egress uplink: vg-<user-id> <-> vh-<user-id> -> host uplink
```

The guest address is `172.16.0.2`, the gateway `172.16.0.1`, and the guest MAC `06:00:ac:10:00:02`, which encodes that address. A warm VM keeps the MAC of the snapshot it resumed from, so a fixed value keeps the reported MAC true.

For `uplink` and `mesh`, Metal derives one transit `/30` from the VM user ID, which removes the need for a persisted address allocator.

## Egress modes

Egress controls internet reachability. It does not control mesh reachability. The veth pair is the private network attachment.

| Mode | veth pair | Default route and NAT | Public IPv4 | VM can reach |
|---|---|---|---|---|
| `uplink` | yes | yes | allowed | mesh peers and the internet |
| `mesh` | yes | no | rejected | mesh peers only |
| `none` | no | no | rejected | nothing |

A change between `uplink` and `mesh` keeps the veth pair. A change to `none` removes it, and the mesh registration with it.

## Throughput limits

The VM configuration can set `private_network_throughput_mibps` and `public_network_throughput_mibps`. Each applies in both directions, and `0` means unlimited.

Private traffic uses the private IPv4 ranges and the mesh IPv6 range. Public traffic uses the remaining IPv4 addresses. The exact filters: [internal/network/SPEC.md](../internal/network/SPEC.md).

A `mesh` VM has no internet path, so Metal keeps a public limit and does not apply it. A `none` VM has no veth pair and receives no limits. The requested values are kept and applied when the veth pair returns.

## Atlas WG Mesh

Each VM has a private IPv6 address in `fdaa::/16`. Atlas WG Mesh routes it between hosts. Metal registers the address when it creates the veth pair and unregisters it when it removes the pair.

Atlas WG Mesh is required. metald refuses to run without the CLI, because a VM with no mesh registration has no private mesh network. On every start it configures an unconfigured host and replays existing VM registrations, so a reinstalled host restores itself without an operator.

Tenant 0 is the privileged tenant. A tenant-0 VM crosses tenants only when its address is in the Atlas WG Mesh whitelist, which `POST /v1/sync` carries in full.

A VM learns its address from MMDS. `atlas-metadata.service` in the guest reads `meta-data/mesh-ipv6` and writes a systemd-networkd drop-in. A timer repeats it, because a warm snapshot resumes with new MMDS content and systemd does not rerun a unit after a resume.

Namespace routing, proxy NDP, MTU, public IPv4 rules, and filter placement: [internal/network/SPEC.md](../internal/network/SPEC.md).

## Packet activity

Metal records the last packet time of each VM, so a sleepy VM can sleep after an idle timeout. One eBPF program hooks the `tap0` egress path, which carries host-to-guest traffic, inside the namespace and stores the time in a map keyed by VM user ID. The program only observes and never changes a packet.

Only host-to-guest frames count, and every such frame counts, including incoming ARP and neighbour discovery, so an incoming frame can wake a sleeping VM before the first IP packet. The guest's own frames arrive on the ingress path and are not hooked, so the guest's housekeeping, such as link-local IPv6, does not keep a sleepy VM awake. A daemon restart starts a new safe baseline, which can delay sleep by one idle timeout but never causes an early sleep. Detail: [internal/network/SPEC.md](../internal/network/SPEC.md).

## WireGuard peers

`POST /v1/sync` supplies the complete managed peer set. Metal applies it to `wg0` and records what it applied, so it never disturbs peers added by other tools.

## Design notes

- A namespace for each VM lets every guest use the same private IPv4 address.
- Deterministic addresses, MAC values, and veth names need no mutable allocator state.
- User-ID-derived veth names fit the Linux interface name limit.
- Egress is one axis: internet reachability. Mesh reachability follows the veth pair.
- A change between `uplink` and `mesh` keeps the interface, so it does not disturb the Atlas WG Mesh hook.
- The namespace routes the mesh address, because Atlas WG Mesh hooks the host end of the veth.
- MMDS carries the mesh address, because the address is per VM and the image is shared.
- Nothing per VM is baked into the image, so one image serves a cold VM and a warm VM.
- Tagged public IPv4 rules permit exact cleanup for one VM.
