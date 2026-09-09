# activity: VM packet activity and network wake

[network SPEC](../SPEC.md) · overview: [docs/networking.md](../../../docs/networking.md)

## Purpose

A sleeping virtual machine (VM) has no Firecracker process, so nothing in user space sees a packet arrive for it. This package puts the observer in the kernel. One eBPF program on each VM tap device records the last host-to-guest packet time, and raises one wake event when the VM manager arms it.

The package owns the eBPF C source, the committed objects, the Go bindings, and the code that loads and attaches them. No other package calls the eBPF runtime.

## Terms

- TCX is the Linux 6.6 hook that attaches a program to one direction of one network device.
- A ring buffer is the shared kernel-to-user queue that carries wake events.

## Types

| Type | Owns |
|---|---|
| `Monitor` | Every eBPF resource. Three shared maps, one program and link for each VM, and the wake worker. Implements `vm.NetworkActivityMonitor` and `vm.NetworkWakeMonitor`. |
| `AttachmentRequest` | The VM ID, user ID, namespace path, and tap device name of one attachment. |
| `bpfActivityLoader` | Creation of the kernel maps and programs. Tests replace it through the `activityLoader` seam. |

## Packet activity

The program attaches to the tap device egress hook in each VM namespace. It does not attach to the veth pair, which Atlas WG Mesh owns.

```text
host TCP -> tap0 egress -> activity_by_user_id[user_id]
                       |
                       +-> armed -> wake event

host non-TCP -> ignored
guest packet  -> tap0 ingress -> not hooked
```

The program reads only the Ethernet type and IP protocol. It counts IPv4 and IPv6 TCP packets, then returns `TCX_NEXT`. It never drops or changes a packet. Other traffic does not count because ARP and IPv6 housekeeping can keep an idle VM awake.

`activity_by_user_id` is a shared hash map keyed by VM user ID. Its value is the monotonic packet time from `bpf_ktime_get_ns`. `LastNetworkActivity` converts its age to UTC, and reports `vm.ErrNotFound` when the given user ID is not the attached one. A wall-clock change cannot make a VM sleep early.

`EnsureAttachment` replaces an attachment when the user ID, the namespace path, or the tap device index changes, and releases the shared map state of a user ID it replaces. `ReleaseAttachment` clears the VM state and closes its program and link. `Close` stops the worker and releases every eBPF resource.

metald does not pin eBPF resources. A restart uses the new attachment time as the activity baseline. This can delay sleep by one idle timeout, but it cannot cause early sleep.

## Packet wake

```text
host TCP -> armed -> notified -> wake_events -> Go worker -> wake channel
```

`wake_state_by_user_id` stores each VM's wake state. `wake_events` is the shared ring buffer. The manager arms a sleeping VM. The first TCP packet atomically changes `armed` to `notified` and writes one event. Later packets do not write another event until the manager rearms the VM.

One worker reads the ring buffer and maps the user ID to the current VM ID. It drops events without a matching attachment. If the output channel is full, it rearms the VM so a later packet can send another event. A read fault is logged and the worker reads again, because one bad record must not end wake for every VM on the host. The worker stops after 64 consecutive faults.

`ReleaseAttachment` clears wake state before a user ID can be reused. The packet that triggers a wake can be lost while Firecracker starts, so clients must retry.

## Generation

`make generate-network` runs bpf2go and rewrites the committed objects and bindings. A normal build never runs Clang. Generation needs Clang 12 or newer. The atomic wake check needs BPF CPU v3 and Linux 6.6 or newer.

## Related

- [network SPEC](../SPEC.md) creates the namespace and the tap device this package hooks.
- [docs/networking.md](../../../docs/networking.md) gives the topology and the reasons behind this design.
- [internal/vm/SPEC.md](../../vm/SPEC.md) defines `NetworkActivityMonitor` and `NetworkWakeMonitor`.
