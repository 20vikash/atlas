# network: VM and host network

[internal SPEC](../SPEC.md) · overview: [docs/networking.md](../../docs/networking.md)

## Purpose

Every virtual machine gets the same private addresses. That is only safe because each VM owns a network namespace, so nothing has to be allocated per VM and nothing has to be remembered between restarts. What cannot be fixed, the veth names and the transit `/30`, is derived from the VM user ID instead of stored.

This package makes one virtual machine network agree with its desired state, and it applies the host WireGuard peer set.

## Terms

- A network namespace is one isolated Linux network stack.
- A TAP connects Firecracker to the guest network.
- A veth pair connects a namespace to the host.
- MASQUERADE gives source network address translation.
- A tc policer drops traffic above a rate. It does not queue it.

## Types

| Type | Owns |
|---|---|
| `LinuxAllocator` | Convergence of one VM network. Implements `vm.Network` and `vm.NetworkActivityMonitor`. |
| `Mesh` | Registration of VM addresses through the Atlas WG Mesh CLI. |
| `WireGuardManager` | The managed peer set of one WireGuard interface. |
| `ActivityMonitor` | VM packet activity tracking. One shared map and one eBPF program for each VM. |

## Convergence

For one virtual machine network, `Ensure` is the only entry point. It never diffs against stored state, because it holds none: it reads the host, and makes the host match.

```text
Ensure
  |
  +- ensureNamespace      create the namespace when absent
  +- ensureNamespaceBase  loopback, TAP, gateway address
  +- removeUnwanted       what this request no longer asks for
  +- addWanted            veth -> internet path -> public IPv4 -> mesh
  +- traffic control      policers on the namespace end of the veth
```

Removal comes before addition, so a change of egress mode never leaves both shapes in place at once. Within `addWanted` the order is load bearing: the veth carries everything above it, and the mesh registration announces the VM, so it is last and the first packet it attracts finds a complete path.

A failed step leaves the partial host state. The next `Ensure` continues from it, so every step must accept a resource that already exists.

| Egress | veth pair | Internet path | Public IPv4 |
|---|---|---|---|
| `uplink` | present | present | optional |
| `mesh` | present | absent | rejected |
| `none` | absent | absent | rejected |

`Release` removes the mesh registration first, because deleting the namespace also deletes the veth pair the registration names.

## Host rules

Public IPv4 needs 5 rules across 2 network stacks: DNAT in, SNAT out, both conntrack directions, and a second DNAT inside the namespace. They are treated as one set. If any rule is missing, all are removed and rewritten, so a partial set from an interrupted run cannot survive.

Every rule carries the comment `metal-public-ipv4-<vm-id>`. Cleanup finds rules by that comment, which is why no rule state is kept anywhere.

`iptables` fails on a duplicate add and on a delete of an absent rule, so each change tests with `-C` first. Exit code 1 means absent; any other failure is an error, so a broken check never reads as absent.

## Traffic control

Policers go on the namespace end of the veth. The host end belongs to Atlas WG Mesh and its terminating direct-action program, so each component owns one end and neither can displace the other.

The VM is inside the namespace, so `egress` carries traffic away from it and matches the destination, while `ingress` carries traffic toward it and matches the source.

Private filters take a lower `tc` priority than the public filter, which matches every IPv4 address. A private packet therefore stops at the private policer. One priority holds one protocol, so private IPv4 and IPv6 need separate priorities.

Private traffic is the RFC 1918 IPv4 ranges `10.0.0.0/8`, `172.16.0.0/12`, and `192.168.0.0/16`, plus the IPv6 unique-local range `fc00::/7`, which contains every mesh prefix. Public traffic is every other IPv4 address.

## Packet activity

`ActivityMonitor` tracks host-to-guest TCP traffic. It attaches one eBPF program to the `tap0` egress hook in each VM namespace. It does not attach to the veth pair, which Atlas WG Mesh owns.

```text
host TCP -> tap0 egress -> activity_by_user_id[user_id]
                       |
                       +-> armed -> wake event

host non-TCP -> ignored
guest packet  -> tap0 ingress -> not hooked
```

The program reads only the Ethernet type and IP protocol. It counts IPv4 and IPv6 TCP packets, then returns `TCX_NEXT`. It never drops or changes a packet. Other traffic does not count because ARP and IPv6 housekeeping can keep an idle VM awake.

`activity_by_user_id` is a shared hash map keyed by VM user ID. Its value is the monotonic packet time from `bpf_ktime_get_ns`. `LastNetworkActivity` converts its age to UTC. A wall-clock change cannot make a VM sleep early.

The monitor owns all maps and one program and link per VM. `EnsureAttachment` replaces an attachment only when the `tap0` index changes. `ReleaseAttachment` clears the VM state and closes its program and link. `Close` stops the worker and releases all eBPF resources.

metald does not pin eBPF resources. A restart uses the new attachment time as the activity baseline. This can delay sleep by one idle timeout, but it cannot cause early sleep.

## Packet wake

A sleeping VM has no Firecracker process to receive a packet. The eBPF program must signal Go to restore the VM.

```text
host TCP -> armed -> notified -> wake_events -> Go worker -> wake channel
```

`wake_state_by_user_id` stores each VM's wake state. `wake_events` is the shared ring buffer. The manager arms a sleeping VM. The first TCP packet atomically changes `armed` to `notified` and writes one event. Later packets do not write another event until the manager rearms the VM.

One worker reads the ring buffer and maps the user ID to the current VM ID. It drops events without a matching attachment. If the output channel is full, it rearms the VM so a later packet can send another event.

`ReleaseAttachment` clears wake state before a user ID can be reused. The packet that triggers a wake can be lost while Firecracker starts, so clients must retry.

`ensureNamespaceBase` adds a static neighbour entry for the fixed guest IP and MAC. This lets a TCP packet reach `tap0` while the guest cannot answer ARP.

## Atlas WG Mesh

Atlas WG Mesh assumes the VM sits directly behind the interface it hooks. A namespace sits between them, so the namespace forwards IPv6 and answers neighbour solicitations for the guest with proxy NDP.

`EnsureHost` configures an unconfigured host and refuses a host that discovers on another interface, because the uplink hook consumes the discovery traffic of every VLAN beneath it.

`ApplyPrivilegedAddresses` and `WireGuardManager.Apply` each replace a complete set. `Apply` drops this host from the peer set it receives and records what it applied, so it never peers with itself and never disturbs peers added by other tools.

## Related

- [docs/networking.md](../../docs/networking.md) gives the topology and the reasons behind this design.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines `Network`.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) attaches Firecracker to the TAP.
