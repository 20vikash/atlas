# Atlas documentation

Each guide covers one topic. Use [SPEC.md](../SPEC.md) for the app layout, the object boundaries, and the component specifications.

## Start here

| Guide                                 | Content                                             |
| ------------------------------------- | --------------------------------------------------- |
| [Getting started](getting-started.md) | Create one test virtual machine from an empty site. |
| [Development](development.md)         | Bench commands, tests, and the local workflow.      |

## Interfaces

| Guide                       | Content                                                                       |
| --------------------------- | ----------------------------------------------------------------------------- |
| [Tenant API](tenant-api.md) | Access rules, request conventions, and how to add a route below `/api/atlas`. |
| [Providers](providers.md)   | The `ServerProvider` extension point and the provider registry.               |
| [Security model](security.md) | Trust boundaries, tenant isolation, and the accepted risks.                 |

## Behavior

| Guide                                                | Content                                                             |
| ---------------------------------------------------- | ------------------------------------------------------------------- |
| [Virtual machine control plane](vm-control-plane.md) | Request, retry, and ownership boundaries between Atlas and Metal.   |
| [Image lifecycle](images.md)                         | System and Machine image creation, transfer, caching, and deletion. |
| [Metal Server lifecycle](metal-server-lifecycle.md)  | Host creation, setup phases, and provider intent.                   |
| [Proxy Server](proxy-server.md)                      | The regional HTTP proxy cluster and its DNS names.                  |
| [Wildcard TLS certificate](wildcard-tls.md)          | The regional certificate and its dns-01 issuance.                   |

## Running Atlas

| Guide                       | Content                                       |
| --------------------------- | --------------------------------------------- |
| [Operations](operations.md) | Fault recovery and the records to read first. |
