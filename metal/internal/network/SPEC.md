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

`ActivityMonitor` records the last packet time of each VM. A sleepy VM sleeps after an idle timeout, so Metal must know when a VM last received a packet from the host side.

One eBPF program attaches to the `tap0` egress path, which carries host-to-guest traffic, inside the VM namespace. The guest's own frames arrive on the ingress path and are not hooked, so the guest's housekeeping does not count as activity. The program does not attach to the veth pair, because Atlas WG Mesh owns the host end.

```text
host-to-guest TCP     -> tap0 egress hook -> activity_by_user_id[user_id]
host-to-guest non-TCP -> tap0 egress hook -> not counted
guest-to-host packet  -> tap0 ingress     -> not hooked, not counted
```

The program only observes. It reads the ethertype and the IP protocol field, then updates the map and returns `TCX_NEXT`, so it never drops or changes a packet. It counts a host-to-guest TCP segment over IPv4 or IPv6 only. Host measurement on the development host showed that link-local IPv6 MLD and ARP housekeeping on the host end of the tap kept a VM awake. So the program counts TCP only. This overrides the first plan that counted every Ethernet frame. A non-TCP frame, such as ARP or neighbour discovery, no longer wakes a VM. A client must retry over TCP.

`activity_by_user_id` is one shared hash map. The key is the VM user ID. The value is a monotonic nanosecond time from `bpf_ktime_get_ns`. `LastNetworkActivity` reads the value, reads `CLOCK_MONOTONIC` right after, and converts the age to a UTC wall-clock time. A wall-clock change never makes a VM sleep early, because the age comes from the monotonic clock.

The monitor owns the shared map, one program instance for each VM, and the egress link. `EnsureAttachment` is idempotent and replaces the attachment only when `tap0` gets a new index. `ReleaseAttachment` closes the link and clears the map value. `Close` releases everything, and the daemon calls it after every worker stops.

metald does not pin the programs or maps. A metald restart attaches new links and starts a new activity baseline. The baseline is the attachment time, so a restart can delay sleep by one idle timeout, which is safe. It never causes an early sleep.

## Atlas WG Mesh

Atlas WG Mesh assumes the VM sits directly behind the interface it hooks. A namespace sits between them, so the namespace forwards IPv6 and answers neighbour solicitations for the guest with proxy NDP.

`EnsureHost` configures an unconfigured host and refuses a host that discovers on another interface, because the uplink hook consumes the discovery traffic of every VLAN beneath it.

`ApplyPrivilegedAddresses` and `WireGuardManager.Apply` each replace a complete set. `Apply` drops this host from the peer set it receives and records what it applied, so it never peers with itself and never disturbs peers added by other tools.

## Related

- [docs/networking.md](../../docs/networking.md) gives the topology and the reasons behind this design.
- [internal/vm/SPEC.md](../vm/SPEC.md) defines `Network`.
- [internal/firecracker/SPEC.md](../firecracker/SPEC.md) attaches Firecracker to the TAP.
