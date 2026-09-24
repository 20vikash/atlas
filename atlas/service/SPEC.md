# Service module specification

[Atlas app specification](../SPEC.md)

## Purpose

The Service module owns Atlas services that run on virtual machines. A service is software for Atlas. It is not a tenant workload.

## Components

- [Proxy Server](doctype/proxy_server/SPEC.md): Creates and configures the regional HTTP proxy.
- [Cargo Server](doctype/cargo_server/SPEC.md): Creates and operates the regional Cargo service.
- [IPv6 Router Server](doctype/ipv6_router_server/SPEC.md): Creates IPv6 routers and gives VMs routed IPv6 addresses.
- [WireGuard Gateway Server](doctype/wireguard_gateway_server/SPEC.md): Creates WireGuard gateways that give customer devices tenant-wide access to private VM addresses.
