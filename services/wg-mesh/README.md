# Atlas WG Mesh

The private network of a region. eBPF hooks on each host give every VM a stable `fdaa::/16` address, enforce tenant isolation, and carry traffic between hosts over WireGuard.

- How it works: [how VMs reach each other](../../docs/networking/index.md)
- Rules, maps, and hooks: [WG Mesh design](../../docs/networking/wg-mesh/index.md)
- CLI commands: [WG Mesh operations](../../docs/networking/wg-mesh/operations.md)
- Build and tests: [WG Mesh development](../../docs/develop/wg-mesh.md)
- Code contract: [SPEC.md](SPEC.md)

Build with `make bpf` and `make build`.

Atlas WG Mesh uses the [AGPL-3.0 license](../../license.txt).
