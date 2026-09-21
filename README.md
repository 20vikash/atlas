<p align="center">
  <img src=".github/assets/logo.svg" alt="Atlas" width="80" height="80">
</p>

<h1 align="center">Atlas</h1>

<p align="center">
  <strong>Virtual machine infrastructure for Frappe Cloud</strong>
</p>

<p align="center">
  <a href="https://github.com/frappe/atlas/actions/workflows/tests.yml"><img src="https://github.com/frappe/atlas/actions/workflows/tests.yml/badge.svg?branch=develop" alt="Tests"></a>
  <a href="https://github.com/frappe/atlas/actions/workflows/linter.yml"><img src="https://github.com/frappe/atlas/actions/workflows/linter.yml/badge.svg?branch=develop" alt="Linter"></a>
  <a href="https://github.com/frappe/atlas/actions/workflows/pages.yml"><img src="https://github.com/frappe/atlas/actions/workflows/pages.yml/badge.svg?branch=develop" alt="Documentation"></a>
  <a href="license.txt"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue.svg" alt="License: AGPL-3.0"></a>
</p>

<p align="center">
  <a href="https://frappe.github.io/atlas/">Documentation</a> ·
  <a href="docs/architecture.md">Architecture</a> ·
  <a href="docs/development.md">Development</a> ·
  <a href="docs/operations.md">Operations</a>
</p>

Atlas helps Frappe Cloud run virtual machines on bare-metal servers. A bare-metal server is a physical server dedicated to Atlas.

Four parts work together. The Atlas app decides what to run and where. Metal runs the VMs. The HTTP proxy sends public web traffic to them. WG Mesh connects them over a private network.

Atlas is in active development and is not deployed to production.

## The four parts

A host is a server that runs virtual machines.

| Part | What it does | Learn online | Read the source docs |
| --- | --- | --- | --- |
| **Atlas app** | Takes the user request and chooses a host. | [Atlas docs](https://frappe.github.io/atlas/atlas/) | [Atlas README](atlas/README.md) |
| **Metal** | Runs the VM on the chosen host. | [Metal docs](https://frappe.github.io/atlas/metal/) | [Metal README](metal/README.md) |
| **HTTP proxy** | Sends public web traffic to the VM. | [Proxy docs](https://frappe.github.io/atlas/services/http-proxy/) | [Proxy README](services/http-proxy/README.md) |
| **WG Mesh** | Sends private traffic between VMs. | [Mesh docs](https://frappe.github.io/atlas/services/wg-mesh/) | [Mesh README](services/wg-mesh/README.md) |

Read the [full documentation site](https://frappe.github.io/atlas/) or browse the [docs folder](docs/) when you want more detail.

## How a VM starts

1. A user asks Atlas for a VM.
2. Atlas checks the request and chooses a host.
3. Atlas tells Metal what the VM needs.
4. Metal prepares the disk, network, and Firecracker VM.
5. The HTTP proxy sends public traffic to the VM.
6. WG Mesh sends private traffic between VMs.

Read [How Atlas works](docs/architecture.md) for the full path, state rules, and failure cases.

## Find your next guide

| If you want to... | Start here |
| --- | --- |
| Create a test VM | [Getting started](atlas/docs/getting-started.md) |
| Change the Atlas app | [Atlas development](atlas/docs/development.md) |
| Change Metal or the VM runtime | [Metal development](metal/docs/development.md) |
| Test Metal on a host | [Metal testing](metal/docs/testing.md) |
| Change public traffic | [HTTP proxy development](services/http-proxy/docs/development.md) |
| Change private VM traffic | [WG Mesh operations](services/wg-mesh/docs/operations.md) |
| Investigate a fault | [Operations guide](docs/operations.md) |
| Understand a term | [Glossary](docs/glossary.md) |

## Work on Atlas

The Atlas app uses Python 3.14, Frappe, MariaDB, Redis, Node, and Yarn.

Metal and WG Mesh use Go 1.26.6 and Linux host features. The HTTP proxy uses Python 3.14 and OpenResty tools.

Run Go commands inside the component that owns the Go module. The repository root is not a Go module.

To work on the documentation site:

```sh
npm install
npm run docs:dev
```

Build the site before you submit a documentation change:

```sh
npm run docs:build
```

Read the [repository specification](SPEC.md) before you change a component boundary. Run the formatter, focused tests, and static checks for the component that you change.

## Contributing

1. [Issue Guidelines](https://github.com/frappe/erpnext/wiki/Issue-Guidelines)
2. [Report Security Vulnerabilities](https://frappe.io/security)

<br>
<br>
<div align="center">
	<a href="https://frappe.io" target="_blank">
		<picture>
			<source media="(prefers-color-scheme: dark)" srcset="https://frappe.io/files/Frappe-white.png">
			<img src="https://frappe.io/files/Frappe-black.png" alt="Frappe Technologies" height="28"/>
		</picture>
	</a>
</div>
