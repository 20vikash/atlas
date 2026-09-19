/* SPDX-License-Identifier: AGPL-3.0 */
/* TC hooks that carry NDP across a routed IPv4 underlay. */
#ifndef ATLAS_NDP_UNICAST_HOOK_H
#define ATLAS_NDP_UNICAST_HOOK_H

#include <linux/ip.h>

#include "debug.h"
#include "state.h"

/* Byte offsets inside the headers the hooks rewrite. */
#define ATLAS_UNICAST_ETHERTYPE_OFFSET 12
#define ATLAS_UNICAST_IPV4_DESTINATION_OFFSET 16
#define ATLAS_UNICAST_IPV4_CHECKSUM_OFFSET 10

/* Address family for IPv4 in bpf_fib_lookup. */
#define ATLAS_UNICAST_AF_INET 2

enum unicast_debug_operation
{
	UNICAST_OPERATION_TX_OWNER = 1,
	UNICAST_OPERATION_TX_FAN_OUT,
	UNICAST_OPERATION_TX_ADVERTISEMENT,
	UNICAST_OPERATION_TX_FAILED,
	UNICAST_OPERATION_RX_REJECTED,
	UNICAST_OPERATION_RX_REQUESTER,
	UNICAST_OPERATION_RX_LEARNED,
	UNICAST_OPERATION_RX_UNKNOWN_PEER,
	UNICAST_OPERATION_RX_KFUNC_FAILED,
};

/* Fold a one's-complement checksum down to 16 bits. */
static __always_inline __u16 atlas_unicast_csum_fold(__u32 sum)
{
	sum = (sum & 0xffff) + (sum >> 16);
	sum = (sum & 0xffff) + (sum >> 16);

	return (__u16)~sum;
}

/* Check if the given IPv4 address belongs to a configured peer. The map key is a separate stack variable, because its address is taken, so the verifier can bound the loop. */
static __always_inline int atlas_unicast_is_known_peer(__be32 peer)
{
	__u32 index;

	for (index = 0; index < ATLAS_UNICAST_PEER_LIMIT; index++) {
		__u32 key = index;

		struct atlas_peer *candidate = bpf_map_lookup_elem(&peer_list, &key);

		if (!candidate || candidate->ipv4 == 0) return 0;

		if (candidate->ipv4 == peer) return 1;
	}

	return 0;
}

/* Wrap a packet in an outer IPv4 header: the header space is added between the Ethernet header and the payload, and the Ethernet type becomes IPv4. The header bytes are stored by the caller per peer. */
static __always_inline int atlas_unicast_wrap_ipv4(struct __sk_buff *packet)
{
	__be16 ethernet_type = bpf_htons(ETH_P_IP);

	if (bpf_skb_adjust_room(packet, sizeof(struct iphdr), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_FIXED_GSO | BPF_F_ADJ_ROOM_ENCAP_L3_IPV4)) return 0;

	return !bpf_skb_store_bytes(packet, ATLAS_UNICAST_ETHERTYPE_OFFSET, &ethernet_type, sizeof(ethernet_type), BPF_F_INVALIDATE_HASH);
}

/* Store the base outer IPv4 header of a wrapped packet. The destination address stays zero, and every peer copy then stores only its own destination address and the checksum contribution that the destination adds. Returns 1 on success and stores the base checksum sum. */
static __always_inline int atlas_unicast_store_base_header(struct __sk_buff *packet, __be32 source_address, __u16 wrapped_length, __s64 *base_sum)
{
	struct iphdr outer = {};

	__s64 header_sum;

	outer.version = 4;
	outer.ihl = 5;
	outer.tot_len = bpf_htons(wrapped_length);
	outer.ttl = 64;
	outer.protocol = IPPROTO_IPV6;
	outer.saddr = source_address;

	header_sum = bpf_csum_diff(NULL, 0, (__be32 *)&outer, sizeof(outer), 0);

	if (header_sum < 0) return 0;

	outer.check = atlas_unicast_csum_fold((__u64)header_sum);

	if (bpf_skb_store_bytes(packet, ETH_HLEN, &outer, sizeof(outer), BPF_F_INVALIDATE_HASH)) return 0;

	*base_sum = header_sum;

	return 1;
}

/* Send one copy of the wrapped packet to a peer: store the peer destination and checksum contribution, resolve the underlay route with bpf_fib_lookup(), and emit the copy with bpf_clone_redirect(). Returns 0 on success. */
static __always_inline int atlas_unicast_send_to_peer(struct __sk_buff *packet, __be32 *peer_address, __be32 source_address, __u16 wrapped_length, __s64 base_sum)
{
	struct bpf_fib_lookup route = {};

	__u16 checksum;

	__s64 peer_sum;

	int fib_result;

	peer_sum = bpf_csum_diff(NULL, 0, (__be32 *)peer_address, sizeof(*peer_address), (__wsum)base_sum);

	if (peer_sum < 0) return -1;

	checksum = atlas_unicast_csum_fold((__u32)peer_sum);

	if (bpf_skb_store_bytes(packet, ETH_HLEN + ATLAS_UNICAST_IPV4_DESTINATION_OFFSET, peer_address, sizeof(*peer_address), BPF_F_INVALIDATE_HASH)) return -1;

	if (bpf_skb_store_bytes(packet, ETH_HLEN + ATLAS_UNICAST_IPV4_CHECKSUM_OFFSET, &checksum, sizeof(checksum), 0)) return -1;

	if (wrapped_length < sizeof(struct iphdr)) return -1;

	/* bpf_fib_lookup() expects tot_len as the host-order L3 length. ifindex is the input L3 device; on success the helper replaces it with the selected egress device. */
	route.family = ATLAS_UNICAST_AF_INET;
	route.tot_len = wrapped_length;
	route.ifindex = packet->ifindex;
	route.ipv4_src = source_address;
	route.ipv4_dst = *peer_address;

	fib_result = bpf_fib_lookup(packet, &route, sizeof(route), BPF_FIB_LOOKUP_OUTPUT);

	if (fib_result) return fib_result;

	if (bpf_skb_store_bytes(packet, 0, route.dmac, ETH_ALEN, 0)) return -1;

	if (bpf_skb_store_bytes(packet, ETH_ALEN, route.smac, ETH_ALEN, 0)) return -1;

	return bpf_clone_redirect(packet, route.ifindex, 0);
}

/* Attached to TC egress on the discovery interface in a unicast environment. Wrapped packets are IPv4, so clones pass through unchanged.
 *
 * A solicitation for a remote VM is wrapped in IPv4 and sent to the last known owner peer from vm_peer_map, or to every peer when no owner is known. The original multicast solicitation is consumed.
 *
 * An advertisement for a local VM is wrapped and returned to the peer that the ingress hook recorded in ndp_requesters.
 */
SEC("tc")
int handle_ndp_unicast_egress(struct __sk_buff *packet)
{
	void *data = (void *)(long)packet->data;
	void *end = (void *)(long)packet->data_end;

	struct ethhdr *eth = data;
	struct ipv6hdr *ip6;
	struct ndp_message *message;

	struct in6_addr target = {};
	struct in6_addr destination = {};

	struct config *local_config;

	__be32 *known_peer;
	__be32 *requester;
	__be32 requester_address;

	__s64 base_sum = 0;

	__u16 wrapped_length;

	__u32 index;

	if ((void *)(eth + 1) > end) return TC_ACT_OK;

	if (eth->h_proto != bpf_htons(ETH_P_IPV6)) return TC_ACT_OK;

	ip6 = (void *)(eth + 1);

	if ((void *)(ip6 + 1) > end || ip6->nexthdr != IPPROTO_ICMPV6) return TC_ACT_OK;

	if (bpf_ntohs(ip6->payload_len) < sizeof(struct ndp_message)) return TC_ACT_OK;

	message = (void *)(ip6 + 1);

	if ((void *)(message + 1) > end) return TC_ACT_OK;

	target = message->target;
	destination = ip6->daddr;

	if (message->icmp.icmp6_type == NDISC_NEIGHBOUR_SOLICITATION) {
		/* Duplicate address detection uses the unspecified source and has no requester that could receive an answer. */
		if (!ip6->saddr.s6_addr32[0] && !ip6->saddr.s6_addr32[1] && !ip6->saddr.s6_addr32[2] && !ip6->saddr.s6_addr32[3]) return TC_ACT_OK;

		/* Only mesh solicitation is transported, and a local target stays on the Linux path. */
		if (!is_virtual_machine_address(&target) || is_local_virtual_machine(&target)) return TC_ACT_OK;

		local_config = get_config();

		if (!local_config) return TC_ACT_OK;

		wrapped_length = sizeof(struct iphdr) + sizeof(struct ipv6hdr) + bpf_ntohs(ip6->payload_len);

		if (!atlas_unicast_wrap_ipv4(packet) || !atlas_unicast_store_base_header(packet, local_config->underlay_ip4, wrapped_length, &base_sum)) {
			emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_FAILED, &target, NULL);
			return TC_ACT_OK;
		}

		known_peer = bpf_map_lookup_elem(&vm_peer_map, &target);

		if (known_peer) {
			/* A known owner receives the only copy. The entry is one shot: it is removed now, so a missing answer makes the next solicitation fan out to every peer. */
			if (atlas_unicast_send_to_peer(packet, known_peer, local_config->underlay_ip4, wrapped_length, base_sum)) emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_FAILED, &target, NULL);
			else emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_OWNER, &target, NULL);

			bpf_map_delete_elem(&vm_peer_map, &target);

			/* The multicast solicitation has no meaning on a routed underlay. */
			return TC_ACT_SHOT;
		}

		/* The map key is a separate stack variable, because its address is taken. No counter is carried across iterations, because a precise loop-carried value stops the verifier from pruning equal iteration states. */
		for (index = 0; index < ATLAS_UNICAST_PEER_LIMIT; index++) {
			__u32 key = index;

			struct atlas_peer *peer = bpf_map_lookup_elem(&peer_list, &key);

			if (!peer || peer->ipv4 == 0) continue;

			if (atlas_unicast_send_to_peer(packet, &peer->ipv4, local_config->underlay_ip4, wrapped_length, base_sum)) emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_FAILED, &target, NULL);
		}

		emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_FAN_OUT, &target, NULL);

		return TC_ACT_SHOT;
	}

	if (message->icmp.icmp6_type != NDISC_NEIGHBOUR_ADVERTISEMENT) return TC_ACT_OK;

	/* Only an answer for a local VM is transported. */
	if (!is_virtual_machine_address(&target) || !is_local_virtual_machine(&target)) return TC_ACT_OK;

	requester = bpf_map_lookup_elem(&ndp_requesters, &destination);

	if (!requester) return TC_ACT_OK;

	local_config = get_config();

	if (!local_config) return TC_ACT_OK;

	wrapped_length = sizeof(struct iphdr) + sizeof(struct ipv6hdr) + bpf_ntohs(ip6->payload_len);

	if (!atlas_unicast_wrap_ipv4(packet) || !atlas_unicast_store_base_header(packet, local_config->underlay_ip4, wrapped_length, &base_sum)) {
		emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_FAILED, &target, NULL);
		return TC_ACT_OK;
	}

	requester_address = *requester;

	if (atlas_unicast_send_to_peer(packet, &requester_address, local_config->underlay_ip4, wrapped_length, base_sum)) {
		emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_FAILED, &target, NULL);
		return TC_ACT_SHOT;
	}

	/* The requester entry is one shot. A later solicitation stores it again. */
	bpf_map_delete_elem(&ndp_requesters, &destination);

	emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_SEND, UNICAST_OPERATION_TX_ADVERTISEMENT, &target, NULL);

	return TC_ACT_SHOT;
}

/* Attached to TC ingress on the discovery interface in a unicast environment. A wrapped NDP packet is accepted only when the outer IPv4 source is a configured peer and the destination is the local underlay address. The outer header is removed, and the inner packet continues to the Linux neighbour discovery path.
 *
 * A solicitation records the outer source in ndp_requesters for the answer. An advertisement records the outer source as the owner in vm_peer_map and registers the VM neighbour from the frame source MAC, so Linux NUD owns liveness.
 */
SEC("tc")
int handle_ndp_unicast_ingress(struct __sk_buff *packet)
{
	void *data = (void *)(long)packet->data;
	void *end = (void *)(long)packet->data_end;

	struct ethhdr *eth = data;
	struct iphdr *ip4;
	struct ipv6hdr *ip6;
	struct ndp_message *message;

	struct in6_addr target = {};
	struct in6_addr source = {};

	struct config *local_config;

	__be16 ethernet_type = bpf_htons(ETH_P_IPV6);

	__u8 type;

	__u64 mac;

	if ((void *)(eth + 1) > end) return TC_ACT_OK;

	if (eth->h_proto != bpf_htons(ETH_P_IP)) return TC_ACT_OK;

	ip4 = (void *)(eth + 1);

	if ((void *)(ip4 + 1) > end) return TC_ACT_OK;

	if (ip4->version != 4 || ip4->ihl != 5) return TC_ACT_OK;

	if (bpf_ntohs(ip4->tot_len) < sizeof(struct iphdr) + sizeof(struct ipv6hdr) + sizeof(struct ndp_message)) return TC_ACT_OK;

	if (ip4->protocol != IPPROTO_IPV6) return TC_ACT_OK;

	local_config = get_config();

	if (!local_config) return TC_ACT_OK;

	if (ip4->daddr != local_config->underlay_ip4) return TC_ACT_OK;

	/* Do not trust an address that is not a configured peer. */
	if (!atlas_unicast_is_known_peer(ip4->saddr)) {
		emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_RECEIVE, UNICAST_OPERATION_RX_REJECTED, &target, NULL);
		return TC_ACT_OK;
	}

	ip6 = (void *)(ip4 + 1);

	if ((void *)(ip6 + 1) > end || ip6->nexthdr != IPPROTO_ICMPV6) return TC_ACT_OK;

	if (bpf_ntohs(ip6->payload_len) < sizeof(struct ndp_message)) return TC_ACT_OK;

	message = (void *)(ip6 + 1);

	if ((void *)(message + 1) > end) return TC_ACT_OK;

	type = message->icmp.icmp6_type;
	target = message->target;
	source = ip6->saddr;

	if (!is_virtual_machine_address(&target)) return TC_ACT_OK;

	if (type == NDISC_NEIGHBOUR_SOLICITATION) {
		/* The outer source is the requester, because the transport only accepts a configured peer. */
		if (!bpf_map_update_elem(&ndp_requesters, &source, &ip4->saddr, BPF_ANY)) emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_RECEIVE, UNICAST_OPERATION_RX_REQUESTER, &target, NULL);
	} else if (type == NDISC_NEIGHBOUR_ADVERTISEMENT) {
		/* Only a remote VM records a location. */
		if (!is_local_virtual_machine(&target)) {
			mac = pack_mac(eth->h_source);

			if (!peer_with_mac(mac)) emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_RECEIVE, UNICAST_OPERATION_RX_UNKNOWN_PEER, &target, NULL);
			else if (!bpf_map_update_elem(&vm_peer_map, &target, &ip4->saddr, BPF_ANY)) {
				if (register_remote_vm(packet->ifindex, &target, mac)) emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_RECEIVE, UNICAST_OPERATION_RX_KFUNC_FAILED, &target, NULL);
				else emit_protocol_debug_event(DEBUG_UNICAST, DEBUG_RECEIVE, UNICAST_OPERATION_RX_LEARNED, &target, NULL);
			}
		}
	} else {
		return TC_ACT_OK;
	}

	/* Remove the outer IPv4 header. The flag also changes skb->protocol back to IPv6, so the Linux receive path picks the inner packet. */
	if (bpf_skb_adjust_room(packet, -(int)sizeof(struct iphdr), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_FIXED_GSO | BPF_F_ADJ_ROOM_DECAP_L3_IPV6)) return TC_ACT_SHOT;

	/* Restore the Ethernet type in the frame bytes, so the inner packet is self consistent for any direct reader. */
	if (bpf_skb_store_bytes(packet, ATLAS_UNICAST_ETHERTYPE_OFFSET, &ethernet_type, sizeof(ethernet_type), 0)) return TC_ACT_SHOT;

	return TC_ACT_OK;
}

#endif /* ATLAS_NDP_UNICAST_HOOK_H */
