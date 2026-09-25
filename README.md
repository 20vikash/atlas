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
  <a href="docs/start/architecture.md">Architecture</a> ·
  <a href="docs/develop/index.md">Development</a> ·
  <a href="docs/operate/find-a-problem.md">Find a problem</a>
</p>

Atlas helps Frappe Cloud run virtual machines on physical servers. The [architecture guide](docs/start/architecture.md) explains the app, host runtime, and network services.

Atlas is in pre-production.

## Start here

| Task | Guide |
| --- | --- |
| Understand the system | [Developer handbook](docs/start/what-atlas-is.md) |
| Set up a test region | [Set up a test region](docs/develop/test-region.md) |
| Make a code change | [Set up development](docs/develop/index.md) and the [code map](docs/develop/code-map.md) |
| Investigate a fault | [Find a problem](docs/operate/find-a-problem.md) |
| Connect a client | [Interfaces](docs/interfaces/index.md) |

## Documentation development

```sh
npm install
npm run docs:dev
```

Run `npm run docs:build` to check links and build the site before you submit a documentation change.

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
