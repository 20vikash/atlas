# traffic: VM packet tracking

For Go code, follow the repository [Go anti-pattern rules](../../../../llm/go-code-review-guide.md).

[network SPEC](../SPEC.md) · overview: [docs/networking.md](../../../docs/networking.md)

## Purpose

This package tracks host-to-guest IP traffic on VM TAP devices. It reports monotonic activity samples and events. It does not decide when a VM must stop or start.

## Ownership

| Type | Owns |
|---|---|
| `Monitor` | VM attachments, traffic samples, the event reader, and the event channel. |
| `bpfHooks` | Shared eBPF maps and one program and TCX link for each attachment. |
| `AttachmentRequest` | The VM target, namespace path, and TAP device name. |
| `Target` | The VM ID and host user ID that identify one attachment. |
| `Sample` | The idle duration and packet sequence from the monotonic clock. |
| `Event` | The target that received traffic while observation was active. |

The network allocator calls `Attach` after it creates the TAP and calls `Detach` before it removes the TAP. The VM manager calls `Sample`, `StartWatching`, and `StopWatching`.

## Packet path

```text
host IPv4 or IPv6 to guest MAC -> tap0 egress -> activity_by_user_id
                                              -> traffic event when watched

ARP or other Ethernet traffic  -> ignored
multicast or broadcast MAC     -> ignored
guest traffic                  -> tap0 ingress -> not hooked
```

The eBPF program reads the Ethernet destination and type, records `bpf_ktime_get_ns`, and can write the user ID to a ring buffer. It returns `TCX_NEXT` and does not change packets.

The program counts only unicast frames. The host IPv6 stack sends multicast listener reports on the TAP device every few seconds. A group address is host control traffic, not guest traffic, so it must not keep an idle VM awake.

The monitor uses the attachment time as the baseline when no packet exists. A wall-clock change cannot make a VM stop early.

`StartWatching` enables events for one target. `StopWatching` disables them. Duplicate events are valid. The VM lock and runtime checks make them safe.

The monitor clears map state when it replaces or removes an attachment. It retries ring buffer read errors with a bounded delay. It delivers each event unless it is closing. `Close` stops the reader and releases every link, program, and map.

`withNetworkNamespace` is the only in-process namespace helper. It stays in `namespace.go` and is used only where TCX needs the TAP interface from the VM namespace.

## Build

`make bpf` compiles `bpf/track_traffic.c` into a local ignored `track_traffic.o`. `make build`, `make test`, and `make vet` build it when needed. Compilation needs Clang, Linux UAPI headers, and libbpf development headers. The Go runtime uses `github.com/cilium/ebpf` and does not use CGO.

## Related

- [network SPEC](../SPEC.md) owns the namespace and TAP lifecycle.
- [internal/vm/SPEC.md](../../vm/SPEC.md) owns idle shutdown and restoration decisions.
