#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ipv6.h>
#include <linux/icmpv6.h>
#include <linux/in6.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

char LICENSE[] SEC("license") = "GPL";

#ifndef NODE
#define NODE 1
#endif

#if NODE == 1

static const struct in6_addr local_public_ip = {
	.s6_addr = {
		0x2a, 0x01, 0x04, 0xf8,
		0x1c, 0x1f, 0xb5, 0x7b,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
};

static const struct in6_addr peer_public_ip = {
	.s6_addr = {
		0x2a, 0x01, 0x04, 0xf8,
		0x0c, 0x0c, 0xcb, 0x8b,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
};

static const struct in6_addr local_ndp_ip = {
	.s6_addr = {
		0xfd, 0xaa, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x99,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
};

static const struct in6_addr remote_ndp_ip = {
	.s6_addr = {
		0xfd, 0xaa, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x99,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x02,
	}
};

#elif NODE == 2

static const struct in6_addr local_public_ip = {
	.s6_addr = {
		0x2a, 0x01, 0x04, 0xf8,
		0x0c, 0x0c, 0xcb, 0x8b,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
};

static const struct in6_addr peer_public_ip = {
	.s6_addr = {
		0x2a, 0x01, 0x04, 0xf8,
		0x1c, 0x1f, 0xb5, 0x7b,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
};

static const struct in6_addr local_ndp_ip = {
	.s6_addr = {
		0xfd, 0xaa, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x99,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x02,
	}
};

static const struct in6_addr remote_ndp_ip = {
	.s6_addr = {
		0xfd, 0xaa, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x99,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
};

#else
#error "NODE must be 1 or 2"
#endif

static __always_inline int
ipv6_equal(const struct in6_addr *a, const struct in6_addr *b)
{
	return a->s6_addr32[0] == b->s6_addr32[0] &&
	       a->s6_addr32[1] == b->s6_addr32[1] &&
	       a->s6_addr32[2] == b->s6_addr32[2] &&
	       a->s6_addr32[3] == b->s6_addr32[3];
}

static __always_inline struct icmp6hdr *
get_icmp6(struct ipv6hdr *ip6, void *data_end)
{
	struct icmp6hdr *icmp6;

	if (ip6->nexthdr != IPPROTO_ICMPV6)
		return NULL;

	icmp6 = (void *)ip6 + sizeof(*ip6);

	if ((void *)(icmp6 + 1) > data_end)
		return NULL;

	return icmp6;
}

/*
 * Egress:
 *
 * Original:
 *
 *   Ethernet
 *   IPv6
 *   ICMPv6 NDP
 *
 * Becomes:
 *
 *   Ethernet
 *   outer IPv6 (Next Header 41)
 *   original IPv6
 *   original ICMPv6 NDP
 *
 * The inner IPv6/ICMPv6 packet is not modified.
 */
SEC("tc/egress")
int ndp_egress(struct __sk_buff *skb)
{
	void *data = (void *)(long)skb->data;
	void *data_end = (void *)(long)skb->data_end;
	struct ethhdr *eth;
	struct ipv6hdr *inner;
	struct icmp6hdr *icmp6;
	struct in6_addr *target;
	__u16 inner_payload_len;
	__u16 outer_payload_len;

	if (data + sizeof(*eth) + sizeof(*inner) > data_end)
		return TC_ACT_OK;

	eth = data;

	if (eth->h_proto != bpf_htons(ETH_P_IPV6))
		return TC_ACT_OK;

	inner = (void *)eth + sizeof(*eth);

	if (inner->version != 6)
		return TC_ACT_OK;

	icmp6 = get_icmp6(inner, data_end);
	if (!icmp6)
		return TC_ACT_OK;

	/*
	 * Both NS and NA carry the logical address in the ICMPv6
	 * Target Address field.
	 *
	 * NS:
	 *   target == remote_ndp_ip
	 *
	 * NA:
	 *   target == local_ndp_ip
	 *
	 * This is important for proxy NDP because the IPv6 source of
	 * the generated NA is normally the local link-local address,
	 * not local_ndp_ip.
	 */
	if (icmp6->icmp6_type != 135 &&
	    icmp6->icmp6_type != 136)
		return TC_ACT_OK;

	target = (void *)icmp6 + sizeof(*icmp6);

	if ((void *)(target + 1) > data_end)
		return TC_ACT_OK;

	if (icmp6->icmp6_type == 135) {
		/*
		 * Neighbor Solicitation for the peer's logical address.
		 */
		if (!ipv6_equal(target, &remote_ndp_ip))
			return TC_ACT_OK;
	} else {
		/*
		 * Neighbor Advertisement generated for our logical/proxy
		 * address.
		 *
		 * Do NOT check inner->saddr here: proxy NDP uses a link-local
		 * IPv6 source for the NA.
		 */
		if (!ipv6_equal(target, &local_ndp_ip))
			return TC_ACT_OK;
	}

	inner_payload_len = bpf_ntohs(inner->payload_len);

	/*
	 * Outer payload = complete inner IPv6 packet.
	 */
	if (inner_payload_len > 65535 - sizeof(struct ipv6hdr))
		return TC_ACT_SHOT;

	outer_payload_len =
		sizeof(struct ipv6hdr) + inner_payload_len;

	/*
	 * Add the outer IPv6 header between Ethernet and the inner IPv6.
	 */
	if (bpf_skb_adjust_room(
		    skb,
		    sizeof(struct ipv6hdr),
		    BPF_ADJ_ROOM_MAC,
		    BPF_F_ADJ_ROOM_ENCAP_L3_IPV6))
		return TC_ACT_SHOT;

	/*
	 * adjust_room invalidates previous packet pointers.
	 * Reload and revalidate everything.
	 */
	data = (void *)(long)skb->data;
	data_end = (void *)(long)skb->data_end;

	if (data + sizeof(*eth) +
	    2 * sizeof(struct ipv6hdr) > data_end)
		return TC_ACT_SHOT;

	eth = data;

	struct ipv6hdr *outer =
		(void *)eth + sizeof(*eth);

	inner =
		(void *)outer + sizeof(*outer);

	if ((void *)(inner + 1) > data_end)
		return TC_ACT_SHOT;

	struct ipv6hdr new_outer = {};

	new_outer.version = 6;
	new_outer.priority = 0;
	new_outer.payload_len = bpf_htons(outer_payload_len);
	new_outer.nexthdr = 41;
	new_outer.hop_limit = 64;
	new_outer.saddr = local_public_ip;
	new_outer.daddr = peer_public_ip;

	/*
	 * Write the outer header directly.
	 *
	 * The bounds check above proves the complete header is writable.
	 */
	__builtin_memcpy(
		outer,
		&new_outer,
		sizeof(new_outer));

	/*
	 * The original NDP packet may have a multicast Ethernet
	 * destination because Linux resolved the solicited-node
	 * multicast IPv6 address.
	 *
	 * bpf_redirect_neigh() requires a non-multicast Ethernet
	 * destination before doing the fresh outer IPv6 FIB/neighbour
	 * lookup.
	 *
	 * Use the source MAC as a temporary placeholder. The helper
	 * performs the real underlay neighbour resolution.
	 */
	__builtin_memcpy(
		eth->h_dest,
		eth->h_source,
		ETH_ALEN);

	return bpf_redirect_neigh(skb->ifindex, NULL, 0, 0);
}

/*
 * Ingress:
 *
 *   Ethernet
 *   outer IPv6 (Next Header 41)
 *   inner IPv6
 *   ICMPv6 NDP
 *
 * Remove only the outer IPv6 header and expose the original packet
 * to the normal IPv6 receive path.
 */
SEC("tc/ingress")
int ndp_ingress(struct __sk_buff *skb)
{
	void *data = (void *)(long)skb->data;
	void *data_end = (void *)(long)skb->data_end;
	struct ethhdr *eth;
	struct ipv6hdr *outer;
	__u16 outer_payload_len;

	if (data + sizeof(*eth) + sizeof(*outer) > data_end)
		return TC_ACT_OK;

	eth = data;

	if (eth->h_proto != bpf_htons(ETH_P_IPV6))
		return TC_ACT_OK;

	outer = (void *)eth + sizeof(*eth);

	if (outer->version != 6)
		return TC_ACT_OK;

	if (outer->nexthdr != 41)
		return TC_ACT_OK;

	if (!ipv6_equal(&outer->daddr, &local_public_ip))
		return TC_ACT_OK;

	if (!ipv6_equal(&outer->saddr, &peer_public_ip))
		return TC_ACT_OK;

	outer_payload_len = bpf_ntohs(outer->payload_len);

	/*
	 * The outer payload must contain at least a complete IPv6 header.
	 */
	if (outer_payload_len < sizeof(struct ipv6hdr))
		return TC_ACT_SHOT;

	if ((void *)outer + sizeof(*outer) +
	    outer_payload_len > data_end)
		return TC_ACT_SHOT;

	/*
	 * Remove only the outer IPv6 header.
	 *
	 * The inner packet remains byte-for-byte unchanged.
	 */
	if (bpf_skb_adjust_room(
		    skb,
		    -(int)sizeof(struct ipv6hdr),
		    BPF_ADJ_ROOM_MAC,
		    BPF_F_ADJ_ROOM_DECAP_L3_IPV6))
		return TC_ACT_SHOT;

	return TC_ACT_OK;
}