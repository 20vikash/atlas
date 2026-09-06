# firecracker/api: REST client over the VM socket

[firecracker SPEC](../SPEC.md) · overview: [docs/architecture.md](../../../docs/architecture.md)

## Purpose

Firecracker serves a REST API on a Unix socket, one socket per virtual machine. This package speaks it with nothing but the standard library: an `http.Client` whose dialer always connects to that one socket, so the URL host carries no meaning and is never resolved.

## Types

| Type | Owns |
|---|---|
| `Client` | One VM's socket, and the lock that serializes requests to it. |
| `Error` | A non-2xx response, carrying the status and Firecracker's fault message. |

The request and response structs mirror the Firecracker API shapes. They add no behavior, so a new endpoint needs a struct and a method and nothing else.

## Transport

```text
send(method, path, body, output):
   URL          http://localhost<path>        host ignored, dialer decides
   dial         unix:<socketPath>
   body         JSON, when present
   2xx          decode into output, when requested
   >= 300       decodeFault -> *Error   "firecracker api: <status>: <fault_message>"
```

Requests to one socket are serialized. Firecracker serves its API from a single thread and rejects a concurrent request, so the lock is held for the whole exchange and is shared by every client built for the same path.

Keep-alives are disabled. A VM is replaced by a new process on the same socket path, and a pooled connection would outlive the process that accepted it.

A failed response still yields an `Error` with its status, even when the body does not decode, because the status is the part a caller acts on.

## Paths

Snapshot and drive paths in requests are resolved by Firecracker inside its jail chroot, not by metald. A caller passes chroot-relative paths and places the files there first.

## Related

- [firecracker SPEC](../SPEC.md) explains how this client fits into boot, snapshot, and stop flows.
- [docs/networking.md](../../../docs/networking.md) explains the MMDS data that the guest reads.
