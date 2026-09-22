/* SPDX-License-Identifier: AGPL-3.0 */
/* TC ingress on wg0, after decryption: deliver tunnels, and repair stale locations with NOT_HERE. */
#ifndef ATLAS_WIREGUARD_H
#define ATLAS_WIREGUARD_H

#include "maps.h"

/*
 * Remove the outer header. Linux routes the inner packet to the VM.
 *
 * GRO can join tunnel packets on wg0. BPF_F_ADJ_ROOM_FIXED_GSO keeps the segment size, or the 1380-byte VM interface rejects a 1420-byte segment.
 */
static __always_inline int remove_mesh_tunnel(struct __sk_buff *packet)
{
	if (bpf_skb_adjust_room(packet, -(int)sizeof(struct ipv6hdr), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_NO_CSUM_RESET | BPF_F_ADJ_ROOM_FIXED_GSO))
		return TC_ACT_SHOT;

	return TC_ACT_OK;
}

/* Replace a tunnel for a VM that is not here with NOT_HERE to its sender. WireGuard encrypts it back to that sender. */
static __always_inline int reply_not_here(struct __sk_buff *packet, struct config *config, const struct in6_addr *virtual_machine, const struct in6_addr *sender)
{
	struct ipv6hdr ip6 = {
		.version = 6,
		.payload_len = bpf_htons(sizeof(*virtual_machine)),
		.nexthdr = NOT_HERE_NEXT_HEADER,
		.hop_limit = HOP_LIMIT,
		.saddr = config->wireguard_ipv6,
		.daddr = *sender,
	};

	if (bpf_skb_change_tail(packet, NOT_HERE_PACKET_LENGTH, 0) ||
		bpf_skb_store_bytes(packet, 0, &ip6, sizeof(ip6), BPF_F_INVALIDATE_HASH) ||
		bpf_skb_store_bytes(packet, sizeof(ip6), virtual_machine, sizeof(*virtual_machine), BPF_F_INVALIDATE_HASH))
		return TC_ACT_SHOT;

	return bpf_redirect(packet->ifindex, 0);
}

/* Forget a location, but only when the host that holds it sends NOT_HERE. Then ask for the new host at once. */
static __always_inline int handle_not_here(struct __sk_buff *packet, struct config *config, const struct in6_addr *sender)
{
	struct in6_addr virtual_machine;
	struct in6_addr *host;

	if (bpf_skb_load_bytes(packet, sizeof(struct ipv6hdr), &virtual_machine, sizeof(virtual_machine)))
		return TC_ACT_SHOT;

	host = bpf_map_lookup_elem(&remote_vms, &virtual_machine);
	if (!host || !is_same_address(host, sender))
		return TC_ACT_SHOT;

	bpf_map_delete_elem(&remote_vms, &virtual_machine);

	/* wg0 has no link layer, so add room for the Ethernet header. */
	if (bpf_skb_change_head(packet, ETH_HLEN, 0) || replace_with_neighbor_solicitation(packet, config->uplink_mac, &config->uplink_ipv6, &virtual_machine))
		return TC_ACT_SHOT;

	return bpf_redirect(config->uplink_ifindex, 0);
}

/* A packet from a remote VM to a client outside the mesh goes to this host's gateway. */
static __always_inline int deliver_to_gateway(struct __sk_buff *packet, const struct in6_addr *source, const struct in6_addr *destination)
{
	struct ethhdr eth = {.h_proto = bpf_htons(ETH_P_IPV6)};
	struct bpf_redir_neigh next_hop = {.nh_family = AF_INET6};
	struct gateway *gateway = get_local_gateway();

	if (!gateway || !is_vm_address(source) || !is_gateway_carried_address(destination))
		return TC_ACT_SHOT;

	__builtin_memcpy(next_hop.ipv6_nh, &gateway->address, sizeof(next_hop.ipv6_nh));

	/* wg0 has no link layer. The neighbor redirect needs an Ethernet header to fill. */
	if (remove_mesh_tunnel(packet) != TC_ACT_OK || bpf_skb_change_head(packet, sizeof(eth), 0) ||
		bpf_skb_store_bytes(packet, 0, &eth, sizeof(eth), 0))
		return TC_ACT_SHOT;

	return bpf_redirect_neigh(gateway->ifindex, &next_hop, sizeof(next_hop), 0);
}

SEC("tc")
int handle_wireguard_packet(struct __sk_buff *packet)
{
	void *end = (void *)(long)packet->data_end;
	struct ipv6hdr *outer = (void *)(long)packet->data;
	struct ipv6hdr *inner = outer + 1;
	struct in6_addr sender, source, destination;
	struct config *config = get_config();

	if (packet->protocol != bpf_htons(ETH_P_IPV6) || (void *)(outer + 1) > end)
		return TC_ACT_OK;
	if (outer->version != 6 || sizeof(*outer) + bpf_ntohs(outer->payload_len) > packet->len)
		return TC_ACT_SHOT;

	if (!config)
		return TC_ACT_SHOT;

	/* Mesh tunnels run between host WireGuard addresses. Other WireGuard traffic is not ours. */
	if (!is_underlay_address(&outer->saddr) || !is_same_address(&outer->daddr, &config->wireguard_ipv6))
		return TC_ACT_OK;

	sender = outer->saddr;

	if (outer->nexthdr == NOT_HERE_NEXT_HEADER)
		return bpf_ntohs(outer->payload_len) == sizeof(struct in6_addr) ? handle_not_here(packet, config, &sender) : TC_ACT_SHOT;

	if (outer->nexthdr != IPPROTO_IPV6)
		return TC_ACT_OK;

	if ((void *)(inner + 1) > end)
		return TC_ACT_SHOT;
	if (sizeof(*inner) + bpf_ntohs(inner->payload_len) > bpf_ntohs(outer->payload_len))
		return TC_ACT_SHOT;

	source = inner->saddr;
	destination = inner->daddr;

	if (!is_vm_address(&destination))
		return deliver_to_gateway(packet, &source, &destination);

	/* The sending host checked source ownership. A foreign source comes from its gateway. */
	if (is_vm_address(&source) && !can_communicate(&source, &destination))
		return TC_ACT_SHOT;

	if (!is_vm_address(&source) && !is_gateway_carried_address(&source))
		return TC_ACT_SHOT;

	if (!is_local_vm(&destination))
		return reply_not_here(packet, config, &destination, &sender);

	return remove_mesh_tunnel(packet);
}

#endif /* ATLAS_WIREGUARD_H */
