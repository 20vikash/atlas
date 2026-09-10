# Networking

For Go code, follow the repository [Go anti-pattern rules](../../llm/go-code-review-guide.md).

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

A VM learns its address from MMDS. `atlas-metadata.service` reads `meta-data/mesh-ipv6` every 250 ms and writes a systemd-networkd drop-in. This updates the address after a warm snapshot resumes.

Namespace routing, proxy NDP, MTU, public IPv4 rules, and filter placement: [internal/network/SPEC.md](../internal/network/SPEC.md).

## Packet activity and wake

Metal tracks TCP traffic from the host to the guest. It ignores other packets and all guest-to-host traffic. This prevents ARP and IPv6 housekeeping from keeping a VM awake.

```text
host TCP packet -> tap0 egress TCX hook -> record activity
                                      |
                                      +-> VM is armed -> send wake event -> restore VM
```

The eBPF program observes packets but does not change them. It stores the last packet time by VM user ID.

The first packet can be lost while Firecracker starts. Clients must retry. A metald restart creates a new activity baseline. This can delay sleep, but it cannot make a VM sleep early. See [internal/network/SPEC.md](../internal/network/SPEC.md).

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
