/* SPDX-License-Identifier: AGPL-3.0 */
/* Atlas WG Mesh state shared by the TC programs and the integration. */
#ifndef ATLAS_STATE_H
#define ATLAS_STATE_H

#include "address.h"

/* VMs connected to this host. The value is the ifindex of the interface that owns the VM, so one VM cannot send traffic with another VM's source address. */
struct
{
	__uint(type, BPF_MAP_TYPE_HASH);
	__type(key, struct in6_addr);
	__type(value, __u32);
	__uint(max_entries, 4096);
} local_vms SEC(".maps");

/* Privileged-tenant addresses allowed to communicate with other tenants. The controller keeps this whitelist in sync on every host. */
struct
{
	__uint(type, BPF_MAP_TYPE_HASH);
	__type(key, struct in6_addr);
	__type(value, __u8);
	__uint(max_entries, 4096);
} privileged_tenant_allowed_addresses SEC(".maps");

/* Learned remote VM-to-WireGuard-host locations, filled from NDP advertisements. */
struct
{
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__type(key, struct in6_addr);
	__type(value, struct in6_addr);
	__uint(max_entries, 262144);
} remote_vms SEC(".maps");

/* Peer capacity of the peer_list map. The unicast hooks use the same limit to bound their loops. */
#define ATLAS_UNICAST_PEER_LIMIT 256

/* One mesh peer, filled from the WireGuard peer state. Peers occupy the low indexes densely; the first zero ipv4 ends the list. */
struct atlas_peer
{
	__be32 ipv4;
	__u8 mac[ETH_ALEN];
	struct in6_addr wg;
};

struct
{
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__type(key, __u32);
	__type(value, struct atlas_peer);
	__uint(max_entries, ATLAS_UNICAST_PEER_LIMIT);
} peer_list SEC(".maps");

/* WireGuard address of the peer that owns each MAC. The NDP hooks identify the answering peer by the frame source MAC of an advertisement. */
struct
{
	__uint(type, BPF_MAP_TYPE_HASH);
	__type(key, __u64);
	__type(value, struct in6_addr);
	__uint(max_entries, ATLAS_UNICAST_PEER_LIMIT);
} peers_by_mac SEC(".maps");

/* Peers that asked this host about a VM. The key is the requesting address of a transported solicitation, and the value is the outer IPv4 source of that solicitation. The unicast egress hook wraps the answer to that peer. */
struct
{
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__type(key, struct in6_addr);
	__type(value, __be32);
	__uint(max_entries, 4096);
} ndp_requesters SEC(".maps");

/* Host configuration. The discovery index records the configured uplink, the underlay IPv4 address sources the unicast NDP transport, and the uplink IPv6 address sources the kernel neighbour solicitation. */
struct config
{
	__u32 discovery_ifindex;
	__be32 underlay_ip4;
	struct in6_addr wg_ip6;
	struct in6_addr uplink_ipv6;
	__u8 discovery_mac[ETH_ALEN];
};

/* One host configuration entry. The integration writes it during setup. */
struct
{
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__type(key, __u32);
	__type(value, struct config);
	__uint(max_entries, 1);
} config SEC(".maps");

struct
{
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__type(key, __u32);
	__type(value, __u8[32]);
	__uint(max_entries, 1);
} build_hash SEC(".maps");

/* Get the current host configuration. */
static __always_inline struct config *get_config(void)
{
	__u32 key = 0;

	return bpf_map_lookup_elem(&config, &key);
}

/* Check if the given address is a local VM. */
static __always_inline int is_local_virtual_machine(const struct in6_addr *virtual_machine)
{
	return bpf_map_lookup_elem(&local_vms, virtual_machine) != NULL;
}

/* True only when the VM is registered on this exact interface. A VM must not send traffic with the source address of a VM on another interface. */
static __always_inline int owns_source_address(const struct in6_addr *virtual_machine, __u32 ifindex)
{
	__u32 *owner = bpf_map_lookup_elem(&local_vms, virtual_machine);

	return owner && *owner == ifindex;
}

/* Cross-tenant traffic is permitted only when one endpoint is a whitelisted privileged-tenant address. This preserves request and response traffic. */
static __always_inline int tenants_can_communicate(const struct in6_addr *source, const struct in6_addr *destination)
{
	if (get_tenant(source) == get_tenant(destination)) return 1;

	if (get_tenant(source) == 0 && bpf_map_lookup_elem(&privileged_tenant_allowed_addresses, source) != NULL) return 1;

	return get_tenant(destination) == 0 && bpf_map_lookup_elem(&privileged_tenant_allowed_addresses, destination) != NULL;
}

/* Pack an Ethernet MAC into the low 6 bytes of a u64, as the kfunc and the peers_by_mac key expect. */
static __always_inline __u64 pack_mac(const __u8 *mac)
{
	__u64 value = 0;

	__builtin_memcpy(&value, mac, ETH_ALEN);

	return value;
}

/* The WireGuard address of the peer that owns this MAC, or NULL. */
static __always_inline struct in6_addr *peer_with_mac(__u64 mac)
{
	return bpf_map_lookup_elem(&peers_by_mac, &mac);
}

/* Get the remote location (WireGuard address of the bare metal host) of the given VM. */
static __always_inline struct in6_addr *get_remote_location(const struct in6_addr *virtual_machine)
{
	return bpf_map_lookup_elem(&remote_vms, virtual_machine);
}

#endif /* ATLAS_STATE_H */
