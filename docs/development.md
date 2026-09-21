# Development setup

Use this guide to choose the smallest environment for your change. Atlas app work needs a Frappe site. Metal and WG Mesh work needs Linux host features.

## Choose an environment

| Work | Start here | Needs |
| --- | --- | --- |
| Atlas app, DocTypes, and placement | [Atlas development](../atlas/docs/development.md) | Frappe, MariaDB, Redis, and a site |
| Metal code and VM runtime | [Metal development](../metal/docs/development.md) | Go and Linux host tools |
| Metal integration tests | [Metal testing](../metal/docs/testing.md) | Linux, root, KVM, ZFS, systemd, and iptables |
| HTTP proxy | [Proxy development](../services/http-proxy/docs/development.md) | Python 3.14 and OpenResty test tools |
| WG Mesh | [Mesh operations](../services/wg-mesh/docs/operations.md) | Linux, Go, Clang, libbpf, and a trusted network |

## Build host binaries

Atlas installs `metald` and Atlas WG Mesh on host VMs. Install the build tools before you use a local site to build them:

```sh
sudo apt-get update
sudo apt-get install --yes make clang libbpf-dev linux-libc-dev
```

Run the build from the Frappe site command used by the [getting started guide](../atlas/docs/getting-started.md).

## Run documentation locally

Install the Node dependencies, then start the VitePress server:

```sh
npm install
npm run docs:dev
```

Build the site before you open a pull request:

```sh
npm run docs:build
```

VitePress reads Markdown from the repository. Do not edit `.vitepress/dist` by hand.

## Before you submit a change

- Read the nearest component `SPEC.md` before a structural change.
- Run the formatter and focused tests for the component.
- Run `npm run docs:build` when you change documentation or the VitePress configuration.
- Keep each page focused on one reader task.
