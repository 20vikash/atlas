# Atlas Repository Specification

## Purpose

Atlas is a monorepo for Frappe Cloud V2 VM infrastructure.

The root contains the Frappe app and three components. They are not separate Git repositories. Each component has its own code, tests, docs, and specification.

## Root layout

```text
atlas/                         Frappe app
clients/                       Generated API clients
metal/                         VM management
llm/                           Review guides for language models
services/http-proxy/           HTTP proxy
services/wg-mesh/              Private VM network
.github/workflows/             CI workflows
.greptile/rules.md             Review rules
CLAUDE.md                      Agent rules
SPEC.md                        This file
```

## Component specifications

- [Atlas app](atlas/SPEC.md): Frappe application and provider catalog.
- [Metal](metal/SPEC.md): Host VM management.
- [HTTP proxy](services/http-proxy/SPEC.md): Regional proxy service.
- [WG Mesh](services/wg-mesh/SPEC.md): Private VM network.

Read the matching specification before you change a component.

`clients/` holds the generated Python clients for the Atlas API and the HTTP proxy control API. See [API clients](docs/api-clients.md).

Each component specification links back to this file.

## Root software

The root app uses Python 3.14, Frappe, MariaDB, Redis, Node, and Yarn.

The root has no `go.mod` or `go.work`. Run Go commands inside the matching component.

## Ownership

Keep component code inside its component. Use a clear API or contract for cross-component work.
