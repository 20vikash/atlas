/* SPDX-License-Identifier: AGPL-3.0 */
/* BPF maps shared by every hook, and their lookups. */
#ifndef ATLAS_MAPS_H
#define ATLAS_MAPS_H

#include "mesh.h"

/* Must match peerLimit in cli/bpf.go. Each peer loop copies its index into a fresh stack key, so the verifier can bound it. */
#define PEER_LIMIT 256

/* Discovery budget per VM interface: 10 per second with a burst of 50. */
#define DISCOVERY_INTERVAL_NS (1000000000ULL / 10)
#define DISCOVERY_BURST 50

/* One unsolicited advertisement for each moved block address each second. */
#define ANNOUNCEMENT_INTERVAL_NS 1000000000ULL

struct config
{
	__u32 uplink_ifindex;
	__u32 public_ifindex;
	__be32 uplink_ipv4;
	struct in6_addr uplink_ipv6;
	struct in6_addr wireguard_ipv6;
	__u8 uplink_mac[ETH_ALEN];
	__u8 public_mac[ETH_ALEN];
};

/* A mesh peer. ipv4 is its address on the uplink network. Peers fill the low indexes, and a zero ipv4 ends the list. */
struct peer
{
	__be32 ipv4;
	__u8 mac[ETH_ALEN];
	__u8 pad[2];
	struct in6_addr wireguard_ipv6;
};

/* The gateway VM of this host. A reply names only its VM and client, so a host runs one gateway. */
struct gateway
{
	__u32 ifindex;
	struct in6_addr address;
};

struct prefix_key
{
	__u32 prefix_length;
	struct in6_addr address;
};

/* A block whose VM left this host. Until expires_ns, the public hook sends its traffic to that VM through the mesh. */
struct moved_prefix
{
	struct in6_addr virtual_machine;
	__u64 expires_ns;
};

/* The destination follows the full VM address, so each VM has its own longest-prefix table. */
struct route_key
{
	__u32 prefix_length;
	struct in6_addr virtual_machine;
	struct in6_addr destination;
};

#define MAP(name, map_type, key_type, value_type, entries) \
	struct                                                  \
	{                                                       \
		__uint(type, map_type);                             \
		__type(key, key_type);                              \
		__type(value, value_type);                          \
		__uint(max_entries, entries);                       \
	} name SEC(".maps")

MAP(config, BPF_MAP_TYPE_ARRAY, __u32, struct config, 1);
MAP(build_hash, BPF_MAP_TYPE_ARRAY, __u32, __u8[32], 1);
MAP(peer_list, BPF_MAP_TYPE_ARRAY, __u32, struct peer, PEER_LIMIT);
MAP(local_gateway, BPF_MAP_TYPE_ARRAY, __u32, struct gateway, 1);

/* Local VM address to the ifindex of its interface. A VM may only send from its own address. */
MAP(local_vms, BPF_MAP_TYPE_HASH, struct in6_addr, __u32, 4096);
/* Tenant-0 addresses that may talk to every tenant. */
MAP(privileged_vms, BPF_MAP_TYPE_HASH, struct in6_addr, __u8, 4096);
/* Remote VM address to the WireGuard address of its host, learned from NDP. */
MAP(remote_vms, BPF_MAP_TYPE_LRU_HASH, struct in6_addr, struct in6_addr, 262144);
/* VM ifindex to the time its next discovery token is due. */
MAP(discovery_limits, BPF_MAP_TYPE_LRU_HASH, __u32, __u64, 4096);
/* Moved block address to the time its next unsolicited advertisement is due. */
MAP(announcement_limits, BPF_MAP_TYPE_LRU_HASH, struct in6_addr, __u64, 4096);

struct
{
	__uint(type, BPF_MAP_TYPE_LPM_TRIE);
	__type(key, struct prefix_key);
	__type(value, __u32);
	__uint(max_entries, 256);
	__uint(map_flags, BPF_F_NO_PREALLOC);
} owned_prefixes SEC(".maps");

struct
{
	__uint(type, BPF_MAP_TYPE_LPM_TRIE);
	__type(key, struct prefix_key);
	__type(value, struct moved_prefix);
	__uint(max_entries, 256);
	__uint(map_flags, BPF_F_NO_PREALLOC);
} moved_prefixes SEC(".maps");

struct
{
	__uint(type, BPF_MAP_TYPE_LPM_TRIE);
	__type(key, struct route_key);
	__type(value, struct in6_addr);
	__uint(max_entries, 8192);
	__uint(map_flags, BPF_F_NO_PREALLOC);
} gateway_routes SEC(".maps");

static __always_inline struct config *get_config(void)
{
	__u32 key = 0;

	return bpf_map_lookup_elem(&config, &key);
}

static __always_inline int is_local_vm(const struct in6_addr *address)
{
	return bpf_map_lookup_elem(&local_vms, address) != NULL;
}

static __always_inline int is_owned_by(const struct in6_addr *address, __u32 ifindex)
{
	__u32 *owner = bpf_map_lookup_elem(&local_vms, address);

	return owner && *owner == ifindex;
}

static __always_inline int is_privileged(const struct in6_addr *address)
{
	return tenant_id(address) == 0 && bpf_map_lookup_elem(&privileged_vms, address) != NULL;
}

/* Tenants are isolated unless one side is a privileged VM. Checking both sides keeps replies working. */
static __always_inline int can_communicate(const struct in6_addr *source, const struct in6_addr *destination)
{
	return tenant_id(source) == tenant_id(destination) || is_privileged(source) || is_privileged(destination);
}

static __always_inline __u32 *get_prefix_owner(const struct in6_addr *address)
{
	struct prefix_key key = {.prefix_length = 128, .address = *address};

	return bpf_map_lookup_elem(&owned_prefixes, &key);
}

/* The VM that took the block of this address, or NULL when the block did not move away from this host. */
static __always_inline struct in6_addr *get_moved_prefix_owner(const struct in6_addr *address)
{
	struct prefix_key key = {.prefix_length = 128, .address = *address};
	struct moved_prefix *moved = bpf_map_lookup_elem(&moved_prefixes, &key);

	return moved && moved->expires_ns > bpf_ktime_get_ns() ? &moved->virtual_machine : NULL;
}

static __always_inline struct in6_addr *get_gateway_route(const struct in6_addr *source, const struct in6_addr *destination)
{
	struct route_key key = {
		.prefix_length = 2 * 8 * sizeof(struct in6_addr),
		.virtual_machine = *source,
		.destination = *destination,
	};

	return bpf_map_lookup_elem(&gateway_routes, &key);
}

/* The gateway VM of this host, or NULL when it runs none. */
static __always_inline struct gateway *get_local_gateway(void)
{
	__u32 key = 0;
	struct gateway *gateway = bpf_map_lookup_elem(&local_gateway, &key);

	return gateway && gateway->ifindex ? gateway : NULL;
}

static __always_inline int is_gateway_interface(__u32 ifindex)
{
	struct gateway *gateway = get_local_gateway();

	return gateway && gateway->ifindex == ifindex;
}

static __always_inline struct peer *find_peer_by_ipv4(__be32 ipv4)
{
	for (__u32 index = 0; index < PEER_LIMIT; index++)
	{
		__u32 key = index;
		struct peer *peer = bpf_map_lookup_elem(&peer_list, &key);

		if (!peer || !peer->ipv4)
			return NULL;
		if (peer->ipv4 == ipv4)
			return peer;
	}

	return NULL;
}

static __always_inline struct peer *find_peer_by_mac(const __u8 *mac)
{
	__u64 packed = pack_mac(mac);

	for (__u32 index = 0; index < PEER_LIMIT; index++)
	{
		__u32 key = index;
		struct peer *peer = bpf_map_lookup_elem(&peer_list, &key);

		if (!peer || !peer->ipv4)
			return NULL;
		if (pack_mac(peer->mac) == packed)
			return peer;
	}

	return NULL;
}

/* Each request moves the next allowed time forward. Refuse a request when the interface uses its burst. */
static __always_inline int take_discovery_token(__u32 ifindex)
{
	__u64 now = bpf_ktime_get_ns();
	__u64 *due = bpf_map_lookup_elem(&discovery_limits, &ifindex);
	__u64 next = due && *due > now ? *due : now;

	if (next - now >= DISCOVERY_BURST * DISCOVERY_INTERVAL_NS)
		return 0;

	next += DISCOVERY_INTERVAL_NS;
	bpf_map_update_elem(&discovery_limits, &ifindex, &next, BPF_ANY);

	return 1;
}

static __always_inline int take_announcement_token(const struct in6_addr *address)
{
	__u64 now = bpf_ktime_get_ns();
	__u64 *due = bpf_map_lookup_elem(&announcement_limits, address);
	__u64 next = now + ANNOUNCEMENT_INTERVAL_NS;

	if (due && *due > now)
		return 0;

	bpf_map_update_elem(&announcement_limits, address, &next, BPF_ANY);

	return 1;
}

/* Replace the packet with a neighbor advertisement from the public interface, which names its MAC for the target. */
static __always_inline int replace_with_public_advertisement(struct __sk_buff *packet, struct config *config, const __u8 *destination_mac, const struct in6_addr *destination, const struct in6_addr *target, __u32 flags)
{
	struct ethhdr eth = {.h_proto = bpf_htons(ETH_P_IPV6)};
	struct ipv6hdr ip6 = {.saddr = *target, .daddr = *destination};
	struct ndp_with_mac advertisement = {
		.icmp.icmp6_type = NDP_ADVERTISEMENT,
		.icmp.icmp6_dataun.un_data32[0] = bpf_htonl(flags),
		.target = *target,
		.option_type = NDP_TARGET_MAC_OPTION,
	};

	__builtin_memcpy(eth.h_dest, destination_mac, ETH_ALEN);
	__builtin_memcpy(eth.h_source, config->public_mac, ETH_ALEN);
	__builtin_memcpy(advertisement.mac, config->public_mac, ETH_ALEN);

	return replace_with_ndp(packet, &eth, &ip6, &advertisement);
}

#endif /* ATLAS_MAPS_H */
