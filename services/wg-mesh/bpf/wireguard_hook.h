/* SPDX-License-Identifier: AGPL-3.0 */
/* TC ingress hook for decrypted WireGuard traffic. */
#ifndef ATLAS_WIREGUARD_HOOK_H
#define ATLAS_WIREGUARD_HOOK_H

#include "debug.h"
#include "state.h"

enum wireguard_debug_operation
{
	WIREGUARD_OPERATION_NOT_HERE_SENT = 1,
	WIREGUARD_OPERATION_NOT_HERE_RECEIVED,
};

/* Tell a sender that its location for one VM is stale. The reply replaces the stale tunnel packet, and the outer destination selects the WireGuard peer that encrypts it back to the sender. */
static __always_inline int send_not_here_message(struct __sk_buff *packet, struct config *local_config, const struct in6_addr *virtual_machine, const struct in6_addr *requesting_host)
{
	struct atlas_msg msg = {};
	struct ipv6hdr ip6 = {};

	ip6.version = 6;
	ip6.payload_len = bpf_htons((__u16)sizeof(msg));
	ip6.nexthdr = ATLAS_CONTROL_NEXT_HEADER;
	ip6.hop_limit = 64;
	ip6.saddr = local_config->wg_ip6;
	ip6.daddr = *requesting_host;

	msg.ver = ATLAS_VER;
	msg.op = MESSAGE_OPERATION_NOT_HERE;
	msg.vm = *virtual_machine;
	msg.host = local_config->wg_ip6;

	emit_protocol_debug_event(DEBUG_WIREGUARD, DEBUG_SEND, WIREGUARD_OPERATION_NOT_HERE_SENT, virtual_machine, &local_config->wg_ip6);

	if (bpf_skb_change_tail(packet, WIREGUARD_CONTROL_PACKET_LENGTH, 0)) return TC_ACT_SHOT;

	if (bpf_skb_store_bytes(packet, 0, &ip6, sizeof(ip6), BPF_F_INVALIDATE_HASH)) return TC_ACT_SHOT;

	if (bpf_skb_store_bytes(packet, WIREGUARD_CONTROL_MESSAGE_OFFSET, &msg, sizeof(msg), BPF_F_INVALIDATE_HASH)) return TC_ACT_SHOT;

	/* packet->ifindex is wg0 here; WireGuard encrypts the reply to the sender. */
	return bpf_redirect(packet->ifindex, 0);
}

/*
 * Attached to TC ingress on wg0, after WireGuard has decrypted the packet.
 *
 * Atlas WG Mesh tunnel packets for a VM on this host have their outer IPv6
 * header removed, so Linux routes the original packet to the VM interface. A
 * tunnel for a VM that this host does not own is stale sender state: the hook
 * answers it with NOT_HERE, so the sender drops its location and discovers
 * the VM again. A received NOT_HERE from the host that the cache holds
 * removes that location. Other WireGuard traffic is left for normal Linux
 * routing.
 */
SEC("tc")
int handle_wireguard_packet(struct __sk_buff *packet)
{
	void *data = (void *)(long)packet->data;
	void *end = (void *)(long)packet->data_end;

	struct ipv6hdr *outer = data;
	struct ipv6hdr *inner;

	struct in6_addr destination_vm;
	struct in6_addr sending_host;

	struct atlas_msg msg = {};

	struct config *local_config;
	struct in6_addr *cached_host;

	if (packet->protocol != bpf_htons(ETH_P_IPV6) || (void *)(outer + 1) > end) return TC_ACT_OK;
	local_config = get_config();
	if (!local_config) return TC_ACT_SHOT;

	/* Atlas tunnels always use host WireGuard addresses as their outer endpoints. */
	if (!is_underlay_address(&outer->saddr) || !are_ipv6_addresses_equal(&outer->daddr, &local_config->wg_ip6)) return TC_ACT_OK;

	sending_host = outer->saddr;

	if (outer->nexthdr == ATLAS_CONTROL_NEXT_HEADER) {
		if (bpf_skb_load_bytes(packet, WIREGUARD_CONTROL_MESSAGE_OFFSET, &msg, sizeof(msg)) || msg.ver != ATLAS_VER || msg.op != MESSAGE_OPERATION_NOT_HERE) return TC_ACT_SHOT;

		/* Only the host that the cache holds may invalidate its own advertisement. */
		cached_host = bpf_map_lookup_elem(&remote_vms, &msg.vm);

		if (cached_host && are_ipv6_addresses_equal(cached_host, &sending_host)) {
			bpf_map_delete_elem(&remote_vms, &msg.vm);

			emit_protocol_debug_event(DEBUG_WIREGUARD, DEBUG_RECEIVE, WIREGUARD_OPERATION_NOT_HERE_RECEIVED, &msg.vm, &sending_host);
		}

		return TC_ACT_SHOT;
	}

	/* Non-Atlas WG Mesh packets received through WireGuard continue normally. */
	if (outer->nexthdr != IPPROTO_IPV6) return TC_ACT_OK;
	inner = (void *)(outer + 1);
	if ((void *)(inner + 1) > end) return TC_ACT_SHOT;
	destination_vm = inner->daddr;
	if (!is_virtual_machine_address(&inner->saddr) || !is_virtual_machine_address(&destination_vm) || !tenants_can_communicate(&inner->saddr, &destination_vm)) return TC_ACT_SHOT;

	if (is_local_virtual_machine(&destination_vm))
	{
		/*
		 * Remove the Atlas WG Mesh outer header. The host route selects the VM interface.
		 *
		 * BPF_F_ADJ_ROOM_FIXED_GSO keeps the segment size unchanged. GRO can
		 * join tunnel packets on wg0. Without this flag, the kernel adds 40
		 * bytes to each segment. The 1380-byte VM interface then rejects the 1420-byte
		 * segment.
		 */
		emit_packet_debug_event(DEBUG_WIREGUARD, DEBUG_ACCEPT, &inner->saddr, &inner->daddr);
		if (bpf_skb_adjust_room(packet, -(int)sizeof(struct ipv6hdr), BPF_ADJ_ROOM_MAC, BPF_F_ADJ_ROOM_NO_CSUM_RESET | BPF_F_ADJ_ROOM_FIXED_GSO)) return TC_ACT_SHOT;
		return TC_ACT_OK;
	}

	emit_packet_debug_event(DEBUG_WIREGUARD, DEBUG_DROP, &inner->saddr, &destination_vm);

	return send_not_here_message(packet, local_config, &destination_vm, &sending_host);
}

#endif /* ATLAS_WIREGUARD_HOOK_H */
