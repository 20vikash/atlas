# Service module specification

[Atlas app specification](../SPEC.md)

## Purpose

The Service module packages Atlas services for virtual machines. A service is software for Atlas. It is not a tenant workload.

The module owns the HTTP proxy package. Atlas creates an archive from `services/http-proxy/` and publishes it as a public File.

## Types

| Type | Owns |
|---|---|
| `http_proxy_package` | The HTTP proxy archive, its source digest, and its public File. |

## Package

```text
services/http-proxy/  -> archive -> public File -> Atlas Settings
```

Git selects archive files according to `.gitignore`. Atlas uses the source digest to skip a build when the source did not change.

Atlas keeps a replaced File until no Atlas Settings link refers to it. See [atlas/atlas/SPEC.md](../atlas/SPEC.md).

## Related

- [HTTP proxy component](../../services/http-proxy/SPEC.md) describes the packaged software.
- [atlas/atlas/SPEC.md](../atlas/SPEC.md) owns the settings fields and the public File helpers.
