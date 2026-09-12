/* SPDX-License-Identifier: AGPL-3.0 */
/* TC ingress hook for every VM interface. */
#ifndef ATLAS_VM_HOOK_H
#define ATLAS_VM_HOOK_H

#include "debug.h"
#include "state.h"

enum vm_debug_operation
{
	DEBUG_VM_UNDERLAY = 1,
	DEBUG_VM_NOT_VIRTUAL,
	DEBUG_VM_SOURCE_NOT_OWNED,
	DEBUG_VM_TENANT_DENIED,
	DEBUG_VM_LOCAL_DESTINATION,
	DEBUG_VM_REMOTE_UNKNOWN,
	DEBUG_VM_NO_CONFIG,
	DEBUG_VM_ENCAP_FAILED,
	DEBUG_VM_ENCAP_SUCCEEDED,
};

static __always_inline void emit_vm_debug_event(
	__u8 verdict,
	__u8 operation,
	const struct in6_addr *source,
	const struct in6_addr *destination)
{
	struct debug_event *event;

	if (!is_debug_enabled())
		return;

	record_debug_stats(verdict, DEBUG_NO_DIRECTION);

	event = bpf_ringbuf_reserve(
		&debug_events,
		sizeof(*event),
		0);

	if (!event)
	{
		record_debug_event_loss();
		return;
	}

	__builtin_memset(event, 0, sizeof(*event));

	event->timestamp = bpf_ktime_get_ns();
	event->hook = DEBUG_VM;
	event->verdict = verdict;
	event->operation = operation;
	event->source = *source;
	event->destination = *destination;

	__builtin_memcpy(
		event->tenant,
		&source->s6_addr[4],
		4);

	bpf_ringbuf_submit(event, 0);
}

static __always_inline int add_tunnel_header(
	struct __sk_buff *packet,
	struct config *local_config,
	const struct in6_addr *remote_host,
	__u16 inner_packet_length)
{
	struct ipv6hdr outer = {};
	long ret;

	outer.version = 6;
	outer.payload_len = bpf_htons(inner_packet_length);
	outer.nexthdr = IPPROTO_IPV6;
	outer.hop_limit = 64;
	outer.saddr = local_config->wg_ip6;
	outer.daddr = *remote_host;

	ret = bpf_skb_adjust_room(
		packet,
		sizeof(outer),
		BPF_ADJ_ROOM_MAC,
		BPF_F_ADJ_ROOM_FIXED_GSO |
		BPF_F_ADJ_ROOM_ENCAP_L3_IPV6 |
		BPF_F_ADJ_ROOM_NO_CSUM_RESET);

	if (ret)
	{
		emit_vm_debug_event(
			DEBUG_DROP,
			DEBUG_VM_ENCAP_FAILED,
			&local_config->wg_ip6,
			remote_host);

		return TC_ACT_SHOT;
	}

	ret = bpf_skb_store_bytes(
		packet,
		ETH_HLEN,
		&outer,
		sizeof(outer),
		BPF_F_INVALIDATE_HASH);

	if (ret)
	{
		emit_vm_debug_event(
			DEBUG_DROP,
			DEBUG_VM_ENCAP_FAILED,
			&local_config->wg_ip6,
			remote_host);

		return TC_ACT_SHOT;
	}

	emit_vm_debug_event(
		DEBUG_REDIRECT,
		DEBUG_VM_ENCAP_SUCCEEDED,
		&outer.saddr,
		&outer.daddr);

	return TC_ACT_OK;
}

/*
 * Attached to TC ingress on every VM interface.
 *
 * Remote VM traffic:
 *
 *   Ethernet
 *   IPv6(inner VM -> VM)
 *          ↓
 *   add outer IPv6 header
 *          ↓
 *   Ethernet
 *   IPv6(outer WG host -> remote WG host)
 *   IPv6(inner VM -> VM)
 *          ↓
 *   Linux routing
 *          ↓
 *   wg0
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

	if ((void *)(eth + 1) > end ||
	    eth->h_proto != bpf_htons(ETH_P_IPV6))
		return TC_ACT_OK;

	ip6 = (void *)(eth + 1);

	if ((void *)(ip6 + 1) > end)
		return TC_ACT_OK;

	src = ip6->saddr;
	dst = ip6->daddr;

	inner_packet_length =
		(__u16)(
			bpf_ntohs(ip6->payload_len) +
			sizeof(*ip6));

	/*
	 * Guests must never inject packets directly to an underlay address.
	 */
	if (is_underlay_address(&dst))
	{
		emit_vm_debug_event(
			DEBUG_DROP,
			DEBUG_VM_UNDERLAY,
			&src,
			&dst);

		return TC_ACT_SHOT;
	}

	/*
	 * Only fdaa::/16 traffic belongs to the mesh.
	 */
	if (!is_virtual_machine_address(&dst))
	{
		emit_vm_debug_event(
			DEBUG_ACCEPT,
			DEBUG_VM_NOT_VIRTUAL,
			&src,
			&dst);

		return TC_ACT_OK;
	}

	/*
	 * Only a registered local VM may inject mesh traffic.
	 */
	if (!owns_source_address(&src, packet->ifindex))
	{
		emit_vm_debug_event(
			DEBUG_DROP,
			DEBUG_VM_SOURCE_NOT_OWNED,
			&src,
			&dst);

		return TC_ACT_SHOT;
	}

	/*
	 * Enforce tenant isolation.
	 */
	if (!tenants_can_communicate(&src, &dst))
	{
		emit_vm_debug_event(
			DEBUG_DROP,
			DEBUG_VM_TENANT_DENIED,
			&src,
			&dst);

		return TC_ACT_SHOT;
	}

	/*
	 * Same-host VM traffic stays on the normal Linux path.
	 */
	if (is_local_virtual_machine(&dst))
	{
		emit_vm_debug_event(
			DEBUG_ACCEPT,
			DEBUG_VM_LOCAL_DESTINATION,
			&src,
			&dst);

		return TC_ACT_OK;
	}

	/*
	 * Find the Atlas host owning the remote VM.
	 */
	remote_host = get_remote_location(&dst);

	if (!remote_host)
	{
		/*
		 * Unknown location: let Linux perform NDP on the
		 * shared VLAN. The NDP hook will learn the owner.
		 */
		emit_vm_debug_event(
			DEBUG_ACCEPT,
			DEBUG_VM_REMOTE_UNKNOWN,
			&src,
			&dst);

		return TC_ACT_OK;
	}

	local_config = get_config();

	if (!local_config)
	{
		emit_vm_debug_event(
			DEBUG_DROP,
			DEBUG_VM_NO_CONFIG,
			&src,
			&dst);

		return TC_ACT_SHOT;
	}

	/*
	 * Remote location is known. Encapsulate the VM packet.
	 */
	return add_tunnel_header(
		packet,
		local_config,
		remote_host,
		inner_packet_length);
}

#endif /* ATLAS_VM_HOOK_H */
