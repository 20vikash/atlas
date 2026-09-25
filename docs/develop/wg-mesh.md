# WG Mesh development

::: warning Local setup is a work in progress
The host setup steps below are not fully verified. Ask the team if a step fails.
:::

WG Mesh has an eBPF data path and a Go command named `atlas-wg-mesh`. Metal calls the command to install hooks and replace host state. Read [how VMs reach each other](../networking/index.md) before you change packet rules.

## Find the owner

| Change | Start here |
| --- | --- |
| Packet checks and forwarding | [VM hook](../../services/wg-mesh/bpf/vm.h) and [WireGuard hook](../../services/wg-mesh/bpf/wireguard.h) |
| Shared map layout | [Map definitions](../../services/wg-mesh/bpf/maps.h) and [Go BPF loader](../../services/wg-mesh/cli/bpf.go) |
| Host and VM commands | [Go command package](../../services/wg-mesh/cli/) |
| Metal integration | [Metal mesh attachment](../../metal/internal/network/mesh.go) |

The [design page](../networking/wg-mesh/index.md) lists every hook, map, and packet rule. The [component specification](../../services/wg-mesh/SPEC.md) defines the code boundaries.

## Build and check

Run these commands from `services/wg-mesh/` on Linux. Install Go from the version in `cli/go.mod`, Clang, and the Linux BPF headers first.

```sh
sudo apt-get update
sudo apt-get install --yes clang libbpf-dev linux-libc-dev
make bpf
go vet -C cli ./...
go test -C cli -race ./...
make build
```

`make bpf` creates the object that the Go command embeds. `make build` creates Linux binaries for amd64 and arm64 in `dist/`. Unit tests do not need root. Host commands need root because they change interfaces and BPF hooks.

## Check a host

Use a test host for packet checks. The [operations guide](../networking/wg-mesh/operations.md) shows how to inspect hooks, maps, peers, and VM locations. Check both local delivery and traffic between hosts when you change a packet rule.

The [WG Mesh CI job](../../.github/workflows/tests.yml) runs the BPF build, Go vet, race tests, and release build. Read the nearest Go test when you change a command. Packet behavior also depends on Metal's host links and WireGuard peer policy.
