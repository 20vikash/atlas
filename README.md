<div align="center">
  <img src=".github/assets/logo.svg" alt="Atlas" width="80" height="80">
  <h1>Atlas</h1>
</div>

Atlas is the virtual machine control system for Frappe Cloud V2.

This monorepo contains the Atlas controller, the Metal host daemon, the HTTP proxy, and Atlas WG Mesh. Each component has its own code, tests, and development guide.

## Start here

1. Read the [system architecture](docs/architecture.md) to understand ownership and the main request flows.
2. If you need a complete virtual machine environment, use [Metal testing](metal/docs/testing.md) to prepare a development host.
3. Choose the component that you want to inspect or change. Its README routes you to its detailed guides and specification.

Use the [glossary](docs/glossary.md) when a term is not clear.

| Component  | Purpose                                                 | First document                              |
| ---------- | ------------------------------------------------------- | ------------------------------------------- |
| Atlas app  | Provider integration, Metal Servers, VM placement, images, and user actions | [Atlas app](atlas/README.md)                |
| Metal      | Virtual machine state and host resources                | [Metal](metal/README.md)                    |
| HTTP proxy | Regional HTTP and TLS routing                           | [HTTP proxy](services/http-proxy/README.md) |
| WG Mesh    | Private virtual machine network                         | [WG Mesh](services/wg-mesh/README.md)       |

Use [Atlas operations](atlas/docs/operations.md) and [Metal operations](metal/docs/operations.md) during fault recovery.

## Development

Each component owns its commands and tests. Do not run Go commands from the repository root.

- Use [Atlas development](atlas/docs/development.md) for the Frappe app.
- Use [Metal development and tests](metal/docs/testing.md) for the Go host daemon.
- Use each service README for its local commands.

Atlas builds and uploads host binaries during installation and migration. Install the [Ubuntu build tools](atlas/docs/development.md#ubuntu-build-tools) first.

## License

Atlas uses the AGPL-3.0 license.
