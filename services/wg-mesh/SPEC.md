# WG Mesh Component Specification

[Root specification](../../SPEC.md)

## Purpose

WG Mesh gives VMs private IPv6 addresses. It routes traffic between bare-metal hosts. It also routes traffic the mesh does not own to a [gateway](docs/gateways.md) VM.

It uses eBPF, WireGuard, Linux NDP with proxy NDP, and NOT_HERE recovery. It serves one region.

## Layout

```text
bpf/                         eBPF hooks: vm.h, wireguard.h, uplink.h, with maps.h and mesh.h
cli/                         Go CLI, one file per command group
docs/                        Design, operations, gateways, and benchmarks
Makefile                     Build targets
```

## Software

The CLI uses Go. It builds for Linux with CGO disabled. The dataplane uses Clang and eBPF.

## Module

The module path is `github.com/frappe/atlas/services/wg-mesh/cli`. Run Go commands from `cli/`.

## Validation

From this directory, run `make bpf` and `make build`. Run `go vet ./...` from `cli/`. The [design](docs/design.md#code-map) maps each file to its hook.

## Documentation

Read the [WG Mesh overview](./) first. Then read the relevant file in [`docs/`](docs/).

## Scope

WG Mesh does not manage peers, keys, NAT, DNS, DHCP, firewalls, or inter-region links.

## Ownership

Keep eBPF code in `bpf/`. Keep CLI code in `cli/`. Keep design and operation docs in `docs/`.
