/* SPDX-License-Identifier: AGPL-3.0 */
/*
 * TC hooks on the uplink and the public interface. They learn VM locations from NDP.
 *
 * Multicast mode: hosts share a VLAN. Linux answers solicitations through proxy NDP, and the ingress hook learns each advertisement.
 * Unicast mode: the egress hook wraps each solicitation and advertisement in IPv4 to every peer, and the ingress hook unwraps it.
 */
#ifndef ATLAS_UPLINK_H
#define ATLAS_UPLINK_H

#include "maps.h"

#define ETHERTYPE_OFFSET 12
#define IPV4_FRAGMENT_BITS 0x3fff

/* Solicited and override, so the router keeps the answer. */
#define ADVERTISEMENT_FLAGS 0x60000000u

/* Answer a solicitation for a prefix that a local VM owns. The provider keeps the prefix on the public link and asks for each address. */
static __always_inline int answer_public_prefix_solicitation(struct __sk_buff *packet, struct config *config, struct ethhdr *eth, struct ipv6hdr *ip6, const struct in6_addr *target)
{
	struct ethhdr reply_eth = {.h_proto = bpf_htons(ETH_P_IPV6)};
	struct ipv6hdr reply = {.saddr = *target, .daddr = ip6->saddr};
	struct ndp_with_mac advertisement = {
		.icmp.icmp6_type = NDP_ADVERTISEMENT,
		.icmp.icmp6_dataun.un_data32[0] = bpf_htonl(ADVERTISEMENT_FLAGS),
		.target = *target,
		.option_type = NDP_TARGET_MAC_OPTION,
	};

	__builtin_memcpy(reply_eth.h_dest, eth->h_source, ETH_ALEN);
	__builtin_memcpy(reply_eth.h_source, config->public_mac, ETH_ALEN);
	__builtin_memcpy(advertisement.mac, config->public_mac, ETH_ALEN);

	if (replace_with_ndp(packet, &reply_eth, &reply, &advertisement))
		return TC_ACT_SHOT;

	return bpf_redirect(packet->ifindex, 0);
}

/* Unicast mode: accept a wrapped NDP packet only from a peer, learn from an advertisement, and unwrap it for Linux. */
static __always_inline int receive_unicast_ndp(struct __sk_buff *packet, struct config *config, void *end)
{
	struct iphdr *ip4 = (void *)(long)packet->data + ETH_HLEN;
	struct ipv6hdr *ip6 = (void *)(ip4 + 1);
	struct ndp_message *message;
	__be16 ethertype = bpf_htons(ETH_P_IPV6);
	struct in6_addr target;
	struct peer *peer;
	__u16 total_length;

	if ((void *)(ip4 + 1) > end || ip4->version != 4 || ip4->ihl != 5 ||
		ip4->protocol != IPPROTO_IPV6 || ip4->daddr != config->uplink_ipv4 ||
		(ip4->frag_off & bpf_htons(IPV4_FRAGMENT_BITS)))
		return TC_ACT_OK;

	total_length = bpf_ntohs(ip4->tot_len);
	if (total_length < sizeof(*ip4) + sizeof(*ip6) + sizeof(struct ndp_message) ||
		ETH_HLEN + total_length > packet->len)
		return TC_ACT_OK;

	message = parse_ndp(ip6, end);
	if (!message || sizeof(*ip4) + sizeof(*ip6) + bpf_ntohs(ip6->payload_len) > total_length)
		return TC_ACT_OK;

	target = message->target;
	if (!is_vm_address(&target))
		return TC_ACT_OK;

	peer = find_peer_by_ipv4(ip4->saddr);
	if (!peer)
		return TC_ACT_SHOT;

	if (message->icmp.icmp6_type == NDP_ADVERTISEMENT && !is_local_vm(&target))
		bpf_map_update_elem(&remote_vms, &target, &peer->wireguard_ipv6, BPF_ANY);

	/* The decap flag also sets the packet protocol back to IPv6. */
	if (bpf_skb_adjust_room(packet, -(int)sizeof(struct iphdr), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_FIXED_GSO | BPF_F_ADJ_ROOM_DECAP_L3_IPV6) ||
		bpf_skb_store_bytes(packet, ETHERTYPE_OFFSET, &ethertype, sizeof(ethertype), 0))
		return TC_ACT_SHOT;

	return TC_ACT_OK;
}

SEC("tc")
int handle_uplink_ingress(struct __sk_buff *packet)
{
	void *end = (void *)(long)packet->data_end;
	struct ethhdr *eth = (void *)(long)packet->data;
	struct config *config = get_config();
	struct ipv6hdr *ip6 = parse_ethernet_ipv6(eth, end);
	struct ndp_message *message;
	struct in6_addr target;
	struct peer *peer;

	if ((void *)(eth + 1) > end || !config)
		return TC_ACT_OK;

	if (eth->h_proto == bpf_htons(ETH_P_IP))
		return receive_unicast_ndp(packet, config, end);

	message = ip6 ? parse_ndp(ip6, end) : NULL;
	if (!message)
		return TC_ACT_OK;

	target = message->target;

	if (message->icmp.icmp6_type == NDP_SOLICITATION)
	{
		if (packet->ifindex == config->public_ifindex && get_prefix_owner(&target))
		{
			if (is_unspecified_address(&ip6->saddr))
				return TC_ACT_OK;
			return answer_public_prefix_solicitation(packet, config, eth, ip6, &target);
		}

		return TC_ACT_OK;
	}

	if (message->icmp.icmp6_type != NDP_ADVERTISEMENT || !is_vm_address(&target) || is_local_vm(&target))
		return TC_ACT_OK;

	/* The frame source MAC names the host that answered. */
	peer = find_peer_by_mac(eth->h_source);
	if (peer)
		bpf_map_update_elem(&remote_vms, &target, &peer->wireguard_ipv6, BPF_ANY);

	return TC_ACT_OK;
}

/* Send a copy of the wrapped packet to one peer. */
static __always_inline void send_unicast_ndp_copy(struct __sk_buff *packet, struct config *config, struct peer *peer, __u16 inner_length)
{
	struct iphdr outer = {
		.version = 4,
		.ihl = 5,
		.tot_len = bpf_htons(sizeof(struct iphdr) + inner_length),
		.ttl = HOP_LIMIT,
		.protocol = IPPROTO_IPV6,
		.saddr = config->uplink_ipv4,
		.daddr = peer->ipv4,
	};
	__s64 checksum;

	checksum = bpf_csum_diff(NULL, 0, (__be32 *)&outer, sizeof(outer), 0);
	if (checksum < 0)
		return;
	outer.check = fold_checksum(checksum);

	if (bpf_skb_store_bytes(packet, ETH_HLEN, &outer, sizeof(outer), BPF_F_INVALIDATE_HASH) ||
		bpf_skb_store_bytes(packet, 0, peer->mac, ETH_ALEN, 0) ||
		bpf_skb_store_bytes(packet, ETH_ALEN, config->uplink_mac, ETH_ALEN, 0))
		return;

	bpf_clone_redirect(packet, packet->ifindex, 0);
}

/* Wrap an NDP packet in IPv4 and send a copy to every peer. */
static __always_inline int send_unicast_ndp_to_peers(struct __sk_buff *packet, struct config *config, __u16 inner_length)
{
	__be16 ethertype = bpf_htons(ETH_P_IP);

	if (bpf_skb_adjust_room(packet, sizeof(struct iphdr), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_FIXED_GSO | BPF_F_ADJ_ROOM_ENCAP_L3_IPV4) ||
		bpf_skb_store_bytes(packet, ETHERTYPE_OFFSET, &ethertype, sizeof(ethertype), BPF_F_INVALIDATE_HASH))
		return TC_ACT_SHOT;

	for (__u32 index = 0; index < PEER_LIMIT; index++)
	{
		__u32 key = index;
		struct peer *peer = bpf_map_lookup_elem(&peer_list, &key);

		if (!peer || !peer->ipv4)
			break;
		send_unicast_ndp_copy(packet, config, peer, inner_length);
	}

	return TC_ACT_SHOT;
}

/* Unicast mode only. Wrapped copies are IPv4, so they pass through this hook unchanged. */
SEC("tc")
int handle_uplink_egress(struct __sk_buff *packet)
{
	void *end = (void *)(long)packet->data_end;
	struct ipv6hdr *ip6 = parse_ethernet_ipv6((void *)(long)packet->data, end);
	struct ndp_message *message = ip6 ? parse_ndp(ip6, end) : NULL;
	struct config *config = get_config();
	struct in6_addr target, source;
	__u32 inner_length;
	int is_local;

	if (!message || !config)
		return TC_ACT_OK;

	/* parse_ndp checked that the whole NDP payload is in the packet. */
	target = message->target;
	source = ip6->saddr;
	inner_length = sizeof(*ip6) + bpf_ntohs(ip6->payload_len);
	if (!is_vm_address(&target))
		return TC_ACT_OK;

	/* A solicitation asks for a remote VM, and an advertisement answers for a local one. Duplicate address detection has no source to answer. */
	is_local = is_local_vm(&target);
	if ((message->icmp.icmp6_type == NDP_SOLICITATION && !is_local && !is_unspecified_address(&source)) ||
		(message->icmp.icmp6_type == NDP_ADVERTISEMENT && is_local))
		return send_unicast_ndp_to_peers(packet, config, inner_length);

	return TC_ACT_OK;
}

#endif /* ATLAS_UPLINK_H */
