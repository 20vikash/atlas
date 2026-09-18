/* SPDX-License-Identifier: AGPL-3.0 */
/* TC ingress hook for the discovery interface: learn remote VM locations. */
#ifndef ATLAS_NDP_HOOK_H
#define ATLAS_NDP_HOOK_H

#include "debug.h"
#include "state.h"

enum ndp_debug_operation
{
	NDP_OPERATION_LEARN = 1,
	NDP_OPERATION_UNKNOWN_PEER,
	NDP_OPERATION_KFUNC_FAILED,
};

/* Attached to TC ingress on the discovery interface. A Neighbor Advertisement
 * for a remote VM carries the answering peer's MAC as the frame source. When
 * that MAC belongs to a configured peer, the VM is registered with that MAC,
 * so Linux NUD owns liveness and bpf_fib_lookup resolves the VM on the VM hook
 * data path.
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

	__u64 mac;

	if ((void *)(eth + 1) > end || eth->h_proto != bpf_htons(ETH_P_IPV6)) return TC_ACT_OK;

	ip6 = (void *)(eth + 1);

	if ((void *)(ip6 + 1) > end || ip6->nexthdr != IPPROTO_ICMPV6) return TC_ACT_OK;

	message = (void *)(ip6 + 1);

	if ((void *)(message + 1) > end) return TC_ACT_OK;

	if (message->icmp.icmp6_type != NDISC_NEIGHBOUR_ADVERTISEMENT) return TC_ACT_OK;

	target = message->target;

	/* Ignore advertisements for own VMs and normal NDP for non-VM addresses. */
	if (!is_virtual_machine_address(&target) || is_local_virtual_machine(&target)) return TC_ACT_OK;

	mac = pack_mac(eth->h_source);

	/* Only a frame from a configured peer records a location. */
	if (!peer_with_mac(mac)) {
		emit_protocol_debug_event(DEBUG_NDP, DEBUG_RECEIVE, NDP_OPERATION_UNKNOWN_PEER, &target, NULL);
		return TC_ACT_OK;
	}

	if (register_remote_vm(packet->ifindex, &target, mac)) {
		emit_protocol_debug_event(DEBUG_NDP, DEBUG_RECEIVE, NDP_OPERATION_KFUNC_FAILED, &target, NULL);
		return TC_ACT_OK;
	}

	emit_protocol_debug_event(DEBUG_NDP, DEBUG_RECEIVE, NDP_OPERATION_LEARN, &target, NULL);

	return TC_ACT_OK;
}

#endif /* ATLAS_NDP_HOOK_H */
