/* SPDX-License-Identifier: AGPL-3.0 */
/* TC ingress hook for every VM interface. */
#ifndef ATLAS_VM_HOOK_H
#define ATLAS_VM_HOOK_H

#include "debug.h"
#include "discovery_limit.h"
#include "state.h"

/* Replace the packet with a bare IPv6 dummy that carries no payload. Routing it through the discovery interface makes the kernel resolve the destination with its own neighbour solicitation. */
static __always_inline int send_discovery_probe(struct __sk_buff *packet, struct config *local_config, const struct in6_addr *destination)
{
	struct ethhdr eth = {};
	struct ipv6hdr ip6 = {};

	__builtin_memcpy(eth.h_dest, local_config->discovery_mac, ETH_ALEN);
	__builtin_memcpy(eth.h_source, local_config->discovery_mac, ETH_ALEN);
	eth.h_proto = bpf_htons(ETH_P_IPV6);

	ip6.version = 6;
	ip6.nexthdr = IPPROTO_NONE;
	ip6.hop_limit = 64;
	ip6.saddr = local_config->uplink_ipv6;
	ip6.daddr = *destination;

	if (bpf_skb_change_tail(packet, ETH_HLEN + sizeof(ip6), 0)) return -1;

	if (bpf_skb_store_bytes(packet, 0, &eth, sizeof(eth), BPF_F_INVALIDATE_HASH)) return -1;

	if (bpf_skb_store_bytes(packet, ETH_HLEN, &ip6, sizeof(ip6), BPF_F_INVALIDATE_HASH)) return -1;

	return 0;
}

static __always_inline int add_tunnel_header(struct __sk_buff *packet, struct config *local_config, const struct in6_addr *remote_host, __u16 inner_packet_length)
{
	struct ipv6hdr outer = {};
	long ret;

	outer.version = 6;
	outer.payload_len = bpf_htons(inner_packet_length);
	outer.nexthdr = IPPROTO_IPV6;
	outer.hop_limit = 64;
	outer.saddr = local_config->wg_ip6;
	outer.daddr = *remote_host;

	ret = bpf_skb_adjust_room(packet, sizeof(outer), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_FIXED_GSO | BPF_F_ADJ_ROOM_ENCAP_L3_IPV6 | BPF_F_ADJ_ROOM_NO_CSUM_RESET);

	if (ret)
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &local_config->wg_ip6, remote_host);

		return TC_ACT_SHOT;
	}

	ret = bpf_skb_store_bytes(packet, ETH_HLEN, &outer, sizeof(outer), BPF_F_INVALIDATE_HASH);

	if (ret)
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &local_config->wg_ip6, remote_host);

		return TC_ACT_SHOT;
	}

	emit_packet_debug_event(DEBUG_VM, DEBUG_REDIRECT, &outer.saddr, &outer.daddr);

	return TC_ACT_OK;
}

/* Attached to TC ingress on every VM interface. Remote VM traffic is
 * encapsulated as Ethernet, IPv6 (outer WG host to remote WG host), IPv6
 * (inner VM to VM), and sent through Linux routing to wg0.
 */
SEC("tc")
int handle_vm_packet(struct __sk_buff *packet)
{
	void *data = (void *)(long)packet->data;
	void *end = (void *)(long)packet->data_end;

	struct ethhdr *eth = data;
	struct ipv6hdr *ip6;

	struct in6_addr src;
	struct in6_addr dst;

	struct config *local_config;
	struct in6_addr *remote_host;

	__u16 inner_packet_length;

	if ((void *)(eth + 1) > end || eth->h_proto != bpf_htons(ETH_P_IPV6)) return TC_ACT_OK;

	ip6 = (void *)(eth + 1);

	if ((void *)(ip6 + 1) > end) return TC_ACT_OK;

	src = ip6->saddr;
	dst = ip6->daddr;

	inner_packet_length = (__u16)(bpf_ntohs(ip6->payload_len) + sizeof(*ip6));

	/* Guests must never inject packets directly to an underlay address. */
	if (is_underlay_address(&dst))
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &src, &dst);

		return TC_ACT_SHOT;
	}

	/* Only fdaa::/16 traffic belongs to the mesh. */
	if (!is_virtual_machine_address(&dst))
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_ACCEPT, &src, &dst);

		return TC_ACT_OK;
	}

	/* Only a registered local VM may inject mesh traffic. */
	if (!owns_source_address(&src, packet->ifindex))
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &src, &dst);

		return TC_ACT_SHOT;
	}

	/* Enforce tenant isolation. */
	if (!tenants_can_communicate(&src, &dst))
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &src, &dst);

		return TC_ACT_SHOT;
	}

	/* Same-host VM traffic stays on the normal Linux path. */
	if (is_local_virtual_machine(&dst))
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_ACCEPT, &src, &dst);

		return TC_ACT_OK;
	}

	local_config = get_config();

	if (!local_config)
	{
		emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &src, &dst);

		return TC_ACT_SHOT;
	}

	/* Find the peer that owns the remote VM. */
	remote_host = get_remote_location(&dst);

	if (!remote_host)
	{
		/* Cap discovery so one guest cannot flood the shared link. */
		if (!discovery_allowed(&src))
		{
			emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &src, &dst);

			return TC_ACT_SHOT;
		}
		/* The remote VM is unknown locally: hand a dummy packet to the host stack, so the kernel resolves the destination with its own neighbour solicitation. */
		if (send_discovery_probe(packet, local_config, &dst))
		{
			emit_packet_debug_event(DEBUG_VM, DEBUG_DROP, &src, &dst);

			return TC_ACT_SHOT;
		}

		emit_packet_debug_event(DEBUG_VM, DEBUG_REDIRECT, &src, &dst);

		return bpf_redirect(local_config->discovery_ifindex, BPF_F_INGRESS);
	}

	/* Remote location is known: encapsulate the VM packet. */
	return add_tunnel_header(packet, local_config, remote_host, inner_packet_length);
}

#endif /* ATLAS_VM_HOOK_H */
