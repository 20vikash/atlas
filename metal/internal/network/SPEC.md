# network: VM and host network

[internal SPEC](../SPEC.md) · overview: [docs/networking.md](../../docs/networking.md)

## Purpose

Package `network` makes each VM network agree with its complete desired state. It also manages host WireGuard peers.

## Terms

- A network namespace is one isolated Linux network stack.
- A TAP connects Firecracker to the guest network.
- A veth pair connects a namespace to the host.
- MASQUERADE gives source network address translation.

## Types

| Type | Role |
|---|---|
| `LinuxAllocator` | Implements `vm.Network` for Linux. |
| `Mesh` | Registers VM addresses through the Atlas WG Mesh CLI. |
| `WireGuardManager` | Applies the complete controller peer set. |

`linux.go` coordinates `Ensure` and `Release`. `namespace.go` owns namespace and virtual Ethernet operations. `internet.go` owns routes and address translation.

## Network state

`Ensure` checks and applies all host resources:

```text
namespace and loopback
TAP and gateway
veth pair and transit addresses
routes and MASQUERADE
public IPv4 rules
mesh registration
traffic control
```

A failed operation keeps the partial host state. The next `Ensure` operation continues from that state.

| Egress | veth pair | Internet path | Public IPv4 |
|---|---|---|---|
| `uplink` | present | present | optional |
| `mesh` | present | absent | absent |
| `none` | absent | absent | absent |

`Release` removes the mesh registration, public IPv4 rules, traffic paths, and namespace.

## Address values

```text
namespace     metal-<vm-id>
TAP           tap0
guest IPv4    172.16.0.2
guest MAC     06:00:ac:10:00:02
gateway       172.16.0.1/24
host veth     vh-<user-id>
guest veth    vg-<user-id>
```

The transit `/30` uses the host user ID. Traffic limits use MiB/s.

## Atlas WG Mesh

The mesh address is in `fdaa::/16`. Atlas WG Mesh attaches to `vh-<user-id>` and sends the guest route through the namespace.

`ApplyPrivilegedAddresses` replaces the complete privileged VM address set. `WireGuardManager.Apply` replaces the complete managed peer set.

## Related

- [docs/networking.md](../../docs/networking.md) gives the network design.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines `Network`.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) attaches Firecracker to the TAP.
