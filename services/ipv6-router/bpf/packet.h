/* SPDX-License-Identifier: AGPL-3.0 */
/* Packet offsets, the L4 checksum location, and address rewrites. */
#ifndef ATLAS_IPV6_ROUTER_PACKET_H
#define ATLAS_IPV6_ROUTER_PACKET_H

#include <stdbool.h>
#include <stddef.h>
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/in.h>
#include <linux/ipv6.h>
#include <bpf/bpf_helpers.h>

#include "address.h"

#define IPV6_OFFSET ETH_HLEN
#define SOURCE_OFFSET (IPV6_OFFSET + offsetof(struct ipv6hdr, saddr))
#define DESTINATION_OFFSET (IPV6_OFFSET + offsetof(struct ipv6hdr, daddr))
#define TRANSPORT_OFFSET (IPV6_OFFSET + sizeof(struct ipv6hdr))

#define FRAGMENT_HEADER_LENGTH 8
#define FRAGMENT_OFFSET_MASK 0xfff8
#define TCP_CHECKSUM_OFFSET 16
#define UDP_CHECKSUM_OFFSET 6
#define ICMPV6_CHECKSUM_OFFSET 2
#define ICMPV6_HEADER_LENGTH 8
/* ICMPv6 types below 128 are errors. An error quotes the packet that caused it. */
#define ICMPV6_ERROR_LIMIT 128
#define NO_CHECKSUM -1

/* Where the L4 header starts and which checksum covers the IPv6 addresses. */
struct transport
{
	__u32 offset;
	__s32 checksum_offset;
	__u64 checksum_flags;
	bool is_icmpv6_error;
};

/* Find the L4 checksum. Follow at most one fragment header. A later fragment has no L4 header.
 * Reject other protocols because an unparsed extension header could hide a checksum.
 */
static __always_inline int find_transport(struct __sk_buff *skb, __u8 next_header, struct transport *transport)
{
	transport->offset = TRANSPORT_OFFSET;
	transport->checksum_offset = NO_CHECKSUM;
	transport->checksum_flags = BPF_F_PSEUDO_HDR;
	transport->is_icmpv6_error = false;

	if (next_header == IPPROTO_FRAGMENT)
	{
		__u8 fragment[4];

		if (bpf_skb_load_bytes(skb, transport->offset, fragment, sizeof(fragment)) < 0)
			return -1;
		if ((((__u16)fragment[2] << 8) | fragment[3]) & FRAGMENT_OFFSET_MASK)
			return 0;

		next_header = fragment[0];
		transport->offset += FRAGMENT_HEADER_LENGTH;
	}

	if (next_header == IPPROTO_TCP)
		transport->checksum_offset = transport->offset + TCP_CHECKSUM_OFFSET;
	else if (next_header == IPPROTO_UDP)
	{
		/* A zero UDP checksum is invalid in IPv6. MARK_MANGLED_0 writes 0xffff instead. */
		transport->checksum_offset = transport->offset + UDP_CHECKSUM_OFFSET;
		transport->checksum_flags |= BPF_F_MARK_MANGLED_0;
	}
	else if (next_header == IPPROTO_ICMPV6)
	{
		__u8 type;

		if (bpf_skb_load_bytes(skb, transport->offset, &type, sizeof(type)) < 0)
			return -1;
		transport->checksum_offset = transport->offset + ICMPV6_CHECKSUM_OFFSET;
		transport->is_icmpv6_error = type < ICMPV6_ERROR_LIMIT;
	}
	else
		return -1;

	return 0;
}

static __always_inline int load_address(struct __sk_buff *skb, __u32 offset, struct address *address)
{
	struct in6_addr raw;

	if (bpf_skb_load_bytes(skb, offset, &raw, sizeof(raw)) < 0)
		return -1;

	*address = to_address(&raw);
	return 0;
}

/* Write an address and apply the incremental change to one checksum. BPF_F_PSEUDO_HDR in
 * checksum_flags means the checksum covers the address through the IPv6 pseudo-header.
 */
static __always_inline int store_address(struct __sk_buff *skb, __u32 offset, const struct address *before,
                                         const struct address *after, __s32 checksum_offset, __u64 checksum_flags)
{
	struct in6_addr old_raw = to_raw(before);
	struct in6_addr new_raw = to_raw(after);

	if (checksum_offset != NO_CHECKSUM)
	{
		__s64 difference = bpf_csum_diff((__be32 *)&old_raw, sizeof(old_raw), (__be32 *)&new_raw, sizeof(new_raw), 0);

		if (difference < 0 || bpf_l4_csum_replace(skb, checksum_offset, 0, difference, checksum_flags) < 0)
			return -1;
	}

	return bpf_skb_store_bytes(skb, offset, &new_raw, sizeof(new_raw), 0);
}

#endif
