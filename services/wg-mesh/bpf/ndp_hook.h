/* SPDX-License-Identifier: AGPL-3.0 */
/* TC hook for the shared VLAN interface: Atlas NDP handling. */
#ifndef ATLAS_NDP_HOOK_H
#define ATLAS_NDP_HOOK_H

#include "debug.h"
#include "state.h"

/* Byte offsets inside the IPv6 header and the ICMPv6 header. */
#define IPV6_PAYLOAD_LENGTH_OFFSET 4
#define ICMPV6_CHECKSUM_OFFSET 2

/* Limit the option walk, so the verifier can bound the loop. */
#define NDP_OPTION_WALK_LIMIT 16

/* Standard NDP Target Link-Layer Address option. */
#define ATLAS_TLLAO_TYPE 2
#define ATLAS_TLLAO_LENGTH 1

/*
 * Additional NDP debug operations.
 *
 * Existing operations 1..6:
 *
 *   1 WHO_HAS
 *   2 FOUND
 *   3 NOT_HERE
 *   4 NOW_HERE
 *   5 ANNOUNCE
 *   6 LEARN
 */
#define NDP_OPERATION_APPEND_NO_CONFIG            7
#define NDP_OPERATION_APPEND_TOO_LARGE            8
#define NDP_OPERATION_APPEND_TAIL_FAILED          9
#define NDP_OPERATION_APPEND_STORE_FAILED        10
#define NDP_OPERATION_APPEND_LENGTH_FAILED       11
#define NDP_OPERATION_APPEND_READ_FAILED         12
#define NDP_OPERATION_APPEND_CSUM_FAILED         13
#define NDP_OPERATION_APPEND_CSUM_STORE_FAILED   14
#define NDP_OPERATION_APPEND_CSUM_REPLACE_FAILED 15
#define NDP_OPERATION_APPEND_SUCCEEDED            16

/*
 * Receive-path debug operations.
 *
 * These deliberately identify every decision point in handle_ndp_packet().
 * They are temporary protocol-trace points for isolating where an NA stops
 * being processed.
 */
#define NDP_OPERATION_RX_START                  17
#define NDP_OPERATION_RX_NOT_IPV6               18
#define NDP_OPERATION_RX_NOT_ICMPV6             19
#define NDP_OPERATION_RX_SHORT_MESSAGE          20
#define NDP_OPERATION_RX_SOLICITATION           21
#define NDP_OPERATION_RX_NOT_ADVERTISEMENT      22
#define NDP_OPERATION_RX_NOT_VM                 23
#define NDP_OPERATION_RX_LOCAL_VM               24
#define NDP_OPERATION_RX_LOCAL_ATLAS_PRESENT   25
#define NDP_OPERATION_RX_REMOTE_VM              26
#define NDP_OPERATION_RX_ATLAS_MISSING          27
#define NDP_OPERATION_RX_HOST_INVALID           28
#define NDP_OPERATION_RX_MAP_UPDATE             29
#define NDP_OPERATION_RX_MAP_UPDATE_FAILED      30
#define NDP_OPERATION_RX_KFUNC_CALL             31
#define NDP_OPERATION_RX_KFUNC_FAILED           32
#define NDP_OPERATION_RX_KFUNC_SUCCEEDED        33

/*
 * Atlas neighbour kfunc v3, provided by the Atlas kernel module.
 *
 * The IPv6 address is passed entirely through scalar arguments:
 *
 *   addr_hi = first 8 bytes of the IPv6 address
 *   addr_lo = last 8 bytes of the IPv6 address
 *
 * The MAC is packed into the low 6 bytes of mac.
 */
extern int atlas_register_neigh_v3(
	__u32 ifindex,
	__u64 addr_hi,
	__u64 addr_lo,
	__u64 mac) __ksym;

/*
 * IPv6 pseudo-header used for ICMPv6 checksum calculation.
 *
 * RFC 8200:
 *
 *   source address       16 bytes
 *   destination address  16 bytes
 *   upper-layer length    4 bytes
 *   zero                  3 bytes
 *   next header           1 byte
 *
 * Total: 40 bytes.
 */
struct atlas_ipv6_pseudo_header {
	struct in6_addr saddr;
	struct in6_addr daddr;
	__be32 length;
	__u8 zero[3];
	__u8 nexthdr;
};

/*
 * Standard NDP Target Link-Layer Address option.
 *
 * Total wire size: 8 bytes.
 */
struct atlas_tllao {
	__u8 type;
	__u8 length;
	__u8 mac[ETH_ALEN];
};

/*
 * Everything appended to the Neighbor Advertisement.
 *
 * TLLAO must be present so Linux NDISC can learn the neighbour's
 * link-layer address. The Atlas option carries the WireGuard address.
 */
struct atlas_ndp_append {
	struct atlas_tllao tllao;
	struct atlas_ndp_option atlas;
};

/*
 * Fold a one's-complement checksum down to 16 bits.
 */
static __always_inline __u16 atlas_csum_fold(__u64 sum)
{
	sum = (sum & 0xffffffff) + (sum >> 32);
	sum = (sum & 0xffffffff) + (sum >> 32);

	sum = (sum & 0xffff) + (sum >> 16);
	sum = (sum & 0xffff) + (sum >> 16);

	return (__u16)~sum;
}

/*
 * Walk the NDP options and copy the Atlas host address into a stack object.
 *
 * Do not return a packet pointer from this helper. Keeping packet pointers
 * inside the bounded loop makes verifier packet-range tracking fragile.
 */
static __always_inline int find_atlas_host(
	struct __sk_buff *packet,
	struct ndp_message *message,
	void *end,
	struct in6_addr *host)
{
	__u8 *cursor = (__u8 *)(message + 1);
	__u8 *limit = end;
	__u8 type;
	__u8 length;
	__u32 option_offset;
	int step;

	for (step = 0; step < NDP_OPTION_WALK_LIMIT; step++) {
		if (cursor + 2 > limit)
			return 0;

		type = cursor[0];
		length = cursor[1];

		if (length == 0)
			return 0;

		if (cursor + length * 8 > limit)
			return 0;

		if (type == ATLAS_NDP_OPTION_TYPE) {
			if (length != ATLAS_NDP_OPTION_LENGTH + 1)
				return 0;

			option_offset =
				ETH_HLEN +
				sizeof(struct ipv6hdr) +
				sizeof(struct ndp_message) +
				(cursor - (__u8 *)(message + 1)) +
				4;

			if (bpf_skb_load_bytes(
				    packet,
				    option_offset,
				    host,
				    sizeof(*host)))
				return 0;

			return 1;
		}

		cursor += length * 8;
	}

	return 0;
}

/*
 * Append the TLLAO and Atlas options to a Neighbor Advertisement.
 *
 * Final packet:
 *
 *   IPv6 header
 *   ICMPv6 Neighbor Advertisement
 *   TLLAO       (8 bytes)
 *   Atlas       (32 bytes)
 *
 * The checksum is calculated from scratch over:
 *
 *   IPv6 pseudo-header
 *   +
 *   complete ICMPv6 message
 *   +
 *   TLLAO
 *   +
 *   Atlas option
 *
 * The checksum field itself is zero while calculating.
 *
 * IMPORTANT:
 *
 * bpf_skb_change_tail() invalidates packet pointers. Every packet-derived
 * value needed after the resize is therefore copied to stack memory first.
 */
static __always_inline int add_atlas_option(
	struct __sk_buff *packet,
	const struct ethhdr *eth,
	const struct ipv6hdr *ip6,
	const struct ndp_message *message)
{
	struct atlas_ndp_append append = {};
	struct atlas_ipv6_pseudo_header pseudo = {};
	struct ndp_message final_message = {};

	struct in6_addr source;
	struct in6_addr destination;
	struct in6_addr target;
	struct in6_addr host;

	__be16 new_checksum;
	__be16 wire_payload_length;

	__u16 old_payload_length;
	__u16 new_payload_length;

	__u32 append_offset;
	__u32 icmp_offset;
	__u32 checksum_offset;
	__u32 append_length;

	__s64 icmp_sum;
	__s64 pseudo_sum;
	__u64 total_sum;

	struct config *local_config;

	local_config = get_config();

	if (!local_config) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_NO_CONFIG,
			&message->target,
			NULL);

		return TC_ACT_OK;
	}

	/*
	 * Build the standard Target Link-Layer Address option.
	 */
	append.tllao.type = ATLAS_TLLAO_TYPE;
	append.tllao.length = ATLAS_TLLAO_LENGTH;

	__builtin_memcpy(
		append.tllao.mac,
		eth->h_source,
		ETH_ALEN);

	/*
	 * Build the Atlas option.
	 */
	append.atlas.type = ATLAS_NDP_OPTION_TYPE;
	append.atlas.length = ATLAS_NDP_OPTION_LENGTH + 1;
	append.atlas.host = local_config->wg_ip6;

	/*
	 * Copy all packet-derived values that will be needed after
	 * bpf_skb_change_tail().
	 */
	source = ip6->saddr;
	destination = ip6->daddr;
	target = message->target;

	/*
	 * Save the Atlas host address on the stack.
	 */
	host = local_config->wg_ip6;

	/*
	 * The complete appended data is:
	 *
	 *   TLLAO  = 8 bytes
	 *   Atlas  = 32 bytes
	 *   Total  = 40 bytes
	 */
	append_length = sizeof(append);

	if (append_length !=
	    sizeof(struct atlas_tllao) +
	    sizeof(struct atlas_ndp_option)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_TOO_LARGE,
			&target,
			&host);

		return TC_ACT_OK;
	}

	old_payload_length =
		bpf_ntohs(ip6->payload_len);

	/*
	 * IPv6 Payload Length is 16 bits.
	 */
	if ((__u32)old_payload_length + append_length > 0xffff) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_TOO_LARGE,
			&target,
			&host);

		return TC_ACT_OK;
	}

	new_payload_length =
		old_payload_length + append_length;

	/*
	 * Append immediately after the fixed Neighbor Advertisement.
	 */
	append_offset =
		ETH_HLEN +
		sizeof(struct ipv6hdr) +
		sizeof(struct ndp_message);

	icmp_offset =
		ETH_HLEN +
		sizeof(struct ipv6hdr);

	checksum_offset =
		icmp_offset +
		ICMPV6_CHECKSUM_OFFSET;

	/*
	 * Announce that the local host is about to extend the NA.
	 */
	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_SEND,
		NDP_OPERATION_ANNOUNCE,
		&target,
		&host);

	/*
	 * Grow the skb for:
	 *
	 *   TLLAO + Atlas option
	 */
	if (bpf_skb_change_tail(
		    packet,
		    packet->len + append_length,
		    0)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_TAIL_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Append the TLLAO and Atlas options.
	 */
	if (bpf_skb_store_bytes(
		    packet,
		    append_offset,
		    &append,
		    append_length,
		    0)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_STORE_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Update the IPv6 Payload Length.
	 */
	wire_payload_length =
		bpf_htons(new_payload_length);

	if (bpf_skb_store_bytes(
		    packet,
		    ETH_HLEN + IPV6_PAYLOAD_LENGTH_OFFSET,
		    &wire_payload_length,
		    sizeof(wire_payload_length),
		    0)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_LENGTH_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Read the fixed Neighbor Advertisement back into stack memory.
	 *
	 * No direct packet access is used after bpf_skb_change_tail().
	 */
	if (bpf_skb_load_bytes(
		    packet,
		    icmp_offset,
		    &final_message,
		    sizeof(final_message))) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_READ_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * The checksum field must be zero while calculating the new checksum.
	 */
	final_message.icmp.icmp6_cksum = 0;

	/*
	 * Calculate the checksum contribution of the fixed
	 * Neighbor Advertisement.
	 */
	icmp_sum =
		bpf_csum_diff(
			NULL,
			0,
			(__be32 *)&final_message,
			sizeof(final_message),
			0);

	if (icmp_sum < 0) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_CSUM_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Add the checksum contribution of:
	 *
	 *   TLLAO + Atlas option
	 */
	icmp_sum =
		bpf_csum_diff(
			NULL,
			0,
			(__be32 *)&append,
			sizeof(append),
			(__wsum)icmp_sum);

	if (icmp_sum < 0) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_CSUM_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Construct the IPv6 pseudo-header.
	 */
	__builtin_memset(&pseudo, 0, sizeof(pseudo));

	pseudo.saddr = source;
	pseudo.daddr = destination;
	pseudo.length = bpf_htonl(new_payload_length);
	pseudo.nexthdr = IPPROTO_ICMPV6;

	/*
	 * Calculate the pseudo-header checksum contribution.
	 */
	pseudo_sum =
		bpf_csum_diff(
			NULL,
			0,
			(__be32 *)&pseudo,
			sizeof(pseudo),
			0);

	if (pseudo_sum < 0) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_CSUM_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Combine:
	 *
	 *   ICMPv6 message
	 *   +
	 *   appended options
	 *   +
	 *   IPv6 pseudo-header
	 */
	total_sum =
		(__u32)icmp_sum +
		(__u32)pseudo_sum;

	/*
	 * Fold the final one's-complement checksum.
	 *
	 * Do NOT call bpf_htons() here.
	 */
	new_checksum =
		atlas_csum_fold(total_sum);

	/*
	 * Write the fully calculated ICMPv6 checksum.
	 *
	 * new_checksum is already the final checksum calculated over the
	 * IPv6 pseudo-header, complete Neighbor Advertisement, and appended
	 * options. Do not use bpf_l4_csum_replace() here: that helper performs
	 * an incremental update based on a changed L4 field.
	 */
	if (bpf_skb_store_bytes(
		    packet,
		    checksum_offset,
		    &new_checksum,
		    sizeof(new_checksum),
		    0)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_SEND,
			NDP_OPERATION_APPEND_CSUM_STORE_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * The complete Neighbor Advertisement was successfully extended.
	 */
	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_SEND,
		NDP_OPERATION_APPEND_SUCCEEDED,
		&target,
		&host);

	return TC_ACT_OK;
}

/*
 * Attached to TC ingress and egress on the shared VLAN interface.
 *
 * Egress:
 *
 *   A Neighbor Advertisement for a local VM is a proxy NDP answer.
 *
 *   Append:
 *
 *     1. Standard TLLAO containing this interface's MAC.
 *     2. Atlas option containing this host's WireGuard IPv6 address.
 *
 * Ingress:
 *
 *   A Neighbor Advertisement containing an Atlas option identifies the
 *   owner of a remote VM. Record that mapping and register the VM in
 *   the Linux neighbour table through the Atlas kfunc.
 */
SEC("tc")
int handle_ndp_packet(struct __sk_buff *packet)
{
	void *data = (void *)(long)packet->data;
	void *end = (void *)(long)packet->data_end;

	struct ethhdr *eth = data;
	struct ipv6hdr *ip6;
	struct ndp_message *message;

	struct in6_addr target = {};
	struct in6_addr host = {};

	__u64 addr_hi = 0;
	__u64 addr_lo = 0;
	__u64 mac = 0;

	int map_result;
	int kfunc_result;

	/*
	 * First trace point. A zero VM address is used until the packet
	 * contains a validated NDP target.
	 */
	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_RECEIVE,
		NDP_OPERATION_RX_START,
		&target,
		NULL);

	/*
	 * Ethernet + IPv6.
	 */
	if ((void *)(eth + 1) > end) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_NOT_IPV6,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	if (eth->h_proto != bpf_htons(ETH_P_IPV6)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_NOT_IPV6,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	ip6 = (void *)(eth + 1);

	/*
	 * IPv6 + ICMPv6.
	 */
	if ((void *)(ip6 + 1) > end) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_NOT_ICMPV6,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	if (ip6->nexthdr != IPPROTO_ICMPV6) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_NOT_ICMPV6,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	message = (void *)(ip6 + 1);

	/*
	 * Fixed Neighbor Discovery message header.
	 */
	if ((void *)(message + 1) > end) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_SHORT_MESSAGE,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	/*
	 * Copy the target as soon as the fixed NDP header is known valid.
	 */
	target = message->target;

	/*
	 * Neighbor Solicitations are passed through unchanged.
	 */
	if (message->icmp.icmp6_type ==
	    NDISC_NEIGHBOUR_SOLICITATION) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_SOLICITATION,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	/*
	 * Only Neighbor Advertisements are handled here.
	 */
	if (message->icmp.icmp6_type !=
	    NDISC_NEIGHBOUR_ADVERTISEMENT) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_NOT_ADVERTISEMENT,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	/*
	 * Ignore normal NDP for non-VM addresses.
	 */
	if (!is_virtual_machine_address(&target)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_NOT_VM,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	/*
	 * Local VM:
	 *
	 * Linux generated an NA for a VM owned by this host.
	 */
	if (is_local_virtual_machine(&target)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_LOCAL_VM,
			&target,
			NULL);

		/*
		 * Don't append the Atlas option twice.
		 */
		if (find_atlas_host(packet, message, end, &host)) {
			emit_protocol_debug_event(
				DEBUG_NDP,
				DEBUG_RECEIVE,
				NDP_OPERATION_RX_LOCAL_ATLAS_PRESENT,
				&target,
				&host);

			return TC_ACT_OK;
		}

		return add_atlas_option(
			packet,
			eth,
			ip6,
			message);
	}

	/*
	 * Remote VM.
	 */
	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_RECEIVE,
		NDP_OPERATION_RX_REMOTE_VM,
		&target,
		NULL);

	/*
	 * Only Atlas advertisements contain the owner information we need.
	 */
	if (!find_atlas_host(packet, message, end, &host)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_ATLAS_MISSING,
			&target,
			NULL);

		return TC_ACT_OK;
	}

	/*
	 * Only accept a valid Atlas underlay address.
	 */
	if (!is_underlay_address(&host)) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_HOST_INVALID,
			&target,
			&host);

		return TC_ACT_OK;
	}

	/*
	 * Atlas discovery succeeded.
	 */
	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_RECEIVE,
		NDP_OPERATION_LEARN,
		&target,
		&host);

	/*
	 * Remember which Atlas host owns this VM.
	 *
	 * BPF_ANY intentionally allows the owner to be replaced if a later
	 * advertisement for the same VM arrives from a different Atlas host.
	 */
	map_result = bpf_map_update_elem(
		&remote_vms,
		&target,
		&host,
		BPF_ANY);

	if (map_result) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_MAP_UPDATE_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_RECEIVE,
		NDP_OPERATION_RX_MAP_UPDATE,
		&target,
		&host);

	/*
	 * Pack the VM IPv6 address into two scalar 64-bit values.
	 *
	 * The kernel kfunc reconstructs the same 16-byte address from
	 * these two values.
	 */
	__builtin_memcpy(
		&addr_hi,
		&target.s6_addr[0],
		sizeof(addr_hi));

	__builtin_memcpy(
		&addr_lo,
		&target.s6_addr[8],
		sizeof(addr_lo));

	/*
	 * Pack the Ethernet source MAC into the low six bytes of mac.
	 *
	 * This is the same L2 address advertised by the remote Atlas host
	 * through the standard TLLAO.
	 */
	__builtin_memcpy(
		&mac,
		eth->h_source,
		ETH_ALEN);

	/*
	 * Register the VM IPv6 address and its L2 address in the Linux
	 * neighbour table through the v3 kfunc.
	 */
	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_RECEIVE,
		NDP_OPERATION_RX_KFUNC_CALL,
		&target,
		&host);

	kfunc_result = atlas_register_neigh_v3(
		packet->ifindex,
		addr_hi,
		addr_lo,
		mac);

	if (kfunc_result) {
		emit_protocol_debug_event(
			DEBUG_NDP,
			DEBUG_RECEIVE,
			NDP_OPERATION_RX_KFUNC_FAILED,
			&target,
			&host);

		return TC_ACT_OK;
	}

	emit_protocol_debug_event(
		DEBUG_NDP,
		DEBUG_RECEIVE,
		NDP_OPERATION_RX_KFUNC_SUCCEEDED,
		&target,
		&host);

	return TC_ACT_OK;
}

#endif /* ATLAS_NDP_HOOK_H */