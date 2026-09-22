# Atlas WG Mesh

Atlas WG Mesh gives each virtual machine a private IPv6 address in `fdaa::/16`.

The address stays with the virtual machine when it moves to another host.

eBPF makes packet decisions, WireGuard encrypts host traffic, and Neighbor Discovery Protocol finds remote virtual machines.

## Packet path

```text
VM A -> VM hook -> WireGuard -> WireGuard hook -> VM B
```

The VM hook sends known remote traffic through WireGuard.

For an unknown address, the hook sends a neighbor solicitation.

The destination host answers through proxy NDP, and the uplink hook records its WireGuard address.

If a virtual machine moves, the old host sends `NOT_HERE`.

The sender removes the old location and starts NDP again.

## Security rules

| Boundary | Rule |
| --- | --- |
| Source | A virtual machine can use only an address owned by its interface. |
| Underlay | A virtual machine cannot reach the host range `fdab::/16`. |
| Tenant | Different tenants cannot communicate. |
| Privileged VM | A listed tenant-0 virtual machine can communicate with all tenants. |
| Discovery | The hooks accept location data only from configured peers. |

NDP does not authenticate a host. Use a trusted host network.

## Documentation

| Document | Purpose |
| --- | --- |
| [Design](docs/design.md) | Packet paths, hooks, and maps. |
| [Operations](docs/operations.md) | All CLI commands and examples. |
| [Gateways](docs/gateways.md) | Public prefixes and routed destinations. |
| [Benchmarks](docs/benchmark.md) | Measured throughput and packet rate. |

Atlas WG Mesh uses the [AGPL-3.0 license](../../license.txt).
