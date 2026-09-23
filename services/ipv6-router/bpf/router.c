/* SPDX-License-Identifier: AGPL-3.0 */
/* tc ingress hook on eth0. It translates each packet without connection state:
 *
 *   destination in the public block  -> destination becomes the mesh address   (Internet to VM)
 *   mesh source, Internet destination -> source becomes the public address     (VM to Internet)
 *
 * The kernel then forwards the packet out of eth0 to the Metal host.
 */
#include <stdbool.h>
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ipv6.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

#include "address.h"
#include "packet.h"

/* An ICMPv6 error quotes the original packet, which went in the other direction. An inbound error
 * quotes our public source address, and an outbound error quotes our mesh destination address.
 * Translate the quoted address so that the receiver can match the error to its connection.
 */
static __always_inline int translate_quoted(struct __sk_buff *skb, const struct transport *transport, bool is_inbound)
{
	__u32 quoted = transport->offset + ICMPV6_HEADER_LENGTH;
	__u32 offset = quoted + (is_inbound ? offsetof(struct ipv6hdr, saddr) : offsetof(struct ipv6hdr, daddr));
	struct address before;
	struct address after;

	/* A truncated quote is not an error. Forward the packet unchanged. */
	if (load_address(skb, offset, &before) < 0)
		return 0;

	if (is_inbound)
	{
		if (!is_mapped_public(&before))
			return 0;
		after = to_private(&before);
	}
	else if (!to_public(&before, &after))
		return 0;

	/* The quote is ICMPv6 payload, not pseudo-header, so no BPF_F_PSEUDO_HDR. */
	return store_address(skb, offset, &before, &after, transport->checksum_offset, 0);
}

SEC("tc")
int ipv6_router_ingress(struct __sk_buff *skb)
{
	void *data = (void *)(long)skb->data;
	void *data_end = (void *)(long)skb->data_end;
	struct ethhdr *ethernet = data;
	struct ipv6hdr *ipv6 = (void *)(ethernet + 1);

	if ((void *)(ipv6 + 1) > data_end || ethernet->h_proto != bpf_htons(ETH_P_IPV6))
		return TC_ACT_OK;

	/* Read everything first. A store invalidates the direct packet pointers. */
	struct address source = to_address(&ipv6->saddr);
	struct address destination = to_address(&ipv6->daddr);
	__u8 next_header = ipv6->nexthdr;

	/* Both can be true: a VM that calls the public address of another VM. */
	bool is_inbound = is_in_block(&destination);
	bool is_outbound = is_mesh(&source) && is_global_unicast(&destination);
	struct address translated;
	struct transport transport;

	if (!is_inbound && !is_outbound)
		return TC_ACT_OK;

	/* A block address without a mapping would loop between this router and its host. */
	if (is_inbound && !is_mapped_public(&destination))
		return TC_ACT_SHOT;

	/* Do not leak a mesh source address that has no public mapping. */
	if (is_outbound && !to_public(&source, &translated))
		return TC_ACT_SHOT;

	if (find_transport(skb, next_header, &transport) < 0)
		return TC_ACT_SHOT;

	if (is_outbound && store_address(skb, SOURCE_OFFSET, &source, &translated, transport.checksum_offset,
	                                 transport.checksum_flags) < 0)
		return TC_ACT_SHOT;

	if (is_inbound)
	{
		translated = to_private(&destination);
		if (store_address(skb, DESTINATION_OFFSET, &destination, &translated, transport.checksum_offset,
		                  transport.checksum_flags) < 0)
			return TC_ACT_SHOT;
	}

	if (transport.is_icmpv6_error && translate_quoted(skb, &transport, is_inbound) < 0)
		return TC_ACT_SHOT;

	return TC_ACT_OK;
}

char LICENSE[] SEC("license") = "GPL";
