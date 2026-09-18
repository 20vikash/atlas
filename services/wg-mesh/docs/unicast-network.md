# Unicast discovery networks

For Go code, follow the repository [Go anti-pattern rules](../../../llm/go-code-review-guide.md).

Atlas WG Mesh normally uses NDP on a shared Layer-2 VLAN. Use the unicast mode when the participating hosts cannot share that Layer-2 domain, for example on providers that route all host traffic.

The unicast mode transports the same NDP packets over the routed IPv4 underlay. A TC egress hook wraps each solicitation or advertisement in an outer IPv4 header, and a TC ingress hook removes that header on the receiving peer. Linux neighbour discovery, proxy NDP, and the WireGuard datapath are unchanged.

## Requirements

- Configure Atlas WG Mesh normally on every host with `atlas-wg-mesh configure`. The Atlas neighbour kernel module is required in unicast mode, exactly as in multicast mode: load it with `atlas-wg-mesh module install`.
- Permit IPv4 protocol 41 between the participating hosts.
- Keep the WireGuard peer state on every host complete. It is the single source of truth: each entry needs an IPv4 endpoint and the uplink MAC of that host, because the hooks identify a peer by MAC and reach it by IPv4.

## Peer identification

No Atlas NDP option exists. Every host knows every peer from the WireGuard peer state:

- An advertisement carries the answering peer's MAC as the frame source. A receiver maps that MAC to the peer through the `peers_by_mac` map, and registers the advertised VM with that MAC.
- A transported solicitation carries the requester in the outer IPv4 source, which the ingress hook already validates against the peer list.
- The VM hook resolves a remote VM with `bpf_fib_lookup`: a hit returns the neighbour MAC, which maps to the owning peer's WireGuard address for encapsulation. A miss crafts a solicitation.

## Manage the peer state

metald writes `wireguard-peers.json` under `base_dir` after every synchronization and reloads the BPF peer maps:

```sh
atlas-wg-mesh peers sync /var/lib/metal/wireguard-peers.json
```

Run the same command by hand after a manual edit. It fills the `peer_list` and `peers_by_mac` maps and installs one neighbour entry per peer, which the kernel resolves for the outer transport. Without metald, an operator writes the JSON state file and runs the command.

## Start the daemon

Run one daemon on every participating host:

```sh
atlas-wg-mesh unicast start /var/lib/metal/wireguard-peers.json
```

The daemon needs at least one peer in the state file. The unicast hooks and the multicast NDP hook never run together. On start, the daemon attaches the two unicast TC hooks to the uplink and removes the multicast NDP filter. On a clean stop, it restores the multicast filter and removes the unicast hooks, and the host returns to multicast behaviour. `SIGTERM` and `SIGINT` use the clean shutdown path. A second daemon exits rather than competing with the active one.

A start or stop passes through a short window where both hook sets are attached. That window is safe: the unicast hooks skip the work that the multicast hook already did, and every learning step is an idempotent replacement.

The daemon performs no packet processing and holds no peer state. All transport runs inside the BPF hooks, and the peer maps come from `peers sync`.

## Liveness

Advertisements register remote VMs through the Atlas kfunc as managed, externally learned neighbour entries, so Linux NUD owns liveness exactly as in multicast mode. The NUD hooks count consecutive failures and remove the neighbour entry after twenty, which makes the next VM packet fail its fib lookup and trigger discovery again.

## Atlas-managed unicast

When the Atlas app manages the host, the controller drives the unicast mode:

1. The `Use Unicast Networking` flag in Atlas Settings switches the region to unicast. The flag is off by default, and the region uses multicast NDP.
2. Every synchronization sends the WireGuard peer set to each host with `POST /v1/sync`. Each entry carries the endpoint and the uplink MAC that the host itself reported in an earlier sync response, so every host knows every peer. In unicast mode the same request carries the `unicast` flag.
3. metald applies the WireGuard peers, reloads the BPF peer maps through `peers sync`, and starts the unicast daemon as a supervised child process when the flag is set. The child stops on the next synchronization without the flag, and its clean stop restores the multicast filters.
4. A metald stop or crash also stops the daemon, because the child runs with a parent-death signal. The next synchronization starts it again.

A host that would keep no peer after metald drops its own entry does not run the daemon, so a single-host region stays in multicast mode.

## How discovery works

1. The host route for `fdaa::/16` still selects the uplink, so Linux sends a multicast solicitation on that interface when a VM location is unknown. The VM hook also crafts such a solicitation when its fib lookup misses.
2. The unicast egress hook captures the solicitation before it reaches the wire. It wraps the packet in an outer IPv4 header and sends one copy to the peer that last answered for the target VM from `vm_peer_map`, or one copy to every peer when no owner is known. Every copy is a `bpf_clone_redirect` clone, so the copies share the packet data.
3. The peer ingress hook accepts the packet only when the outer IPv4 source is a configured peer. It removes the outer header, records the requester for the answer, and hands the solicitation to Linux, which answers through proxy NDP.
4. The egress hook wraps the answer and returns it to the recorded requester alone.
5. The requester ingress hook removes the outer header. It records the owning peer in `vm_peer_map` and registers the neighbour entry through the Atlas kfunc, so Linux NUD owns liveness exactly as in multicast mode.

The owner entry is one shot: the egress hook removes it when it sends the solicitation. When the owner never answers, for example after a VM moved to another host, the next solicitation finds no entry and fans out to every peer. The new owner answers, and the map points to it again.

Run `atlas-wg-mesh upgrade` normally while the daemon is running, then let the next synchronization restart the daemon so it attaches the programs from the new release. The upgrade preserves the unicast maps; a release that changes the peer map layouts rebuilds them through `peers sync`.
