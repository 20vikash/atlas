# Atlas app

The regional control plane for Frappe Cloud VMs: a Frappe app that manages hosts, VM placement, images, public IPs, and service VMs. Metal runs the VMs. This app decides what runs where.

- How it works: the [developer handbook](../docs/start/what-atlas-is.md)
- Codebase guide and dev setup: [Atlas app](../docs/develop/atlas-app.md)
- Code contract: [SPEC.md](SPEC.md)

| Module | Owns | Specification |
| --- | --- | --- |
| `atlas` | Settings, providers, DNS, TLS, and host binaries | [atlas/SPEC.md](atlas/SPEC.md) |
| `metal_server` | Provider hosts, Metal installation, capacity, and public IPs | [metal_server/SPEC.md](metal_server/SPEC.md) |
| `vm` | Placement, VM records, images, migration, and the console bridge | [vm/SPEC.md](vm/SPEC.md) |
| `service` | Proxy, Cargo, and IPv6 router service VMs | [service/SPEC.md](service/SPEC.md) |
