/* SPDX-License-Identifier: AGPL-3.0 */
/*
 * Atlas WG Mesh NUD failure tracking.
 *
 * Tracks consecutive NUD failures for remote VMs.
 *
 *   NUD_REACHABLE
 *       -> failure count = 0
 *
 *   NUD_FAILED
 *       -> failure count++
 *
 *   20 consecutive failures
 *       -> remove VM from remote_vms
 *       -> clear the Linux neighbour entry for garbage collection
 *       -> remove failure counter
 */
#ifndef ATLAS_NUD_HOOK_H
#define ATLAS_NUD_HOOK_H

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ipv6.h>
#include <linux/neighbour.h>

#include "state.h"

#define ATLAS_NUD_FAILURE_LIMIT 20

/* The uapi headers carry no address families, and the value is a stable ABI. */
#ifndef AF_INET6
#define AF_INET6 10
#endif

/*
 * Raw tracepoint context for:
 *
 *     tracepoint/neigh/neigh_update
 *
 * Layout matches:
 *
 *     /sys/kernel/tracing/events/neigh/neigh_update/format
 */
struct trace_event_raw_neigh_update
{
	__u16 common_type;
	__u8 common_flags;
	__u8 common_preempt_count;
	__s32 common_pid;

	__u32 family;
	__u32 dev;

	__u8 lladdr[32];
	__u8 lladdr_len;

	__u8 flags;
	__u8 nud_state;
	__u8 type;
	__u8 dead;

	__s32 refcnt;

	__u8 primary_key4[4];
	__u8 primary_key6[16];

	unsigned long confirmed;
	unsigned long updated;
	unsigned long used;

	__u8 new_lladdr[32];
	__u8 new_state;

	__u32 update_flags;
	__u32 pid;
};

/*
 * Kernel kfunc provided by the Atlas neighbour module. The delete clears the
 * managed and externally-learned flags, and the neighbour garbage collector
 * then removes the entry.
 */
extern int atlas_delete_neigh(
	__u32 ifindex,
	__u64 addr_hi,
	__u64 addr_lo) __ksym;


static __always_inline void get_nud_address(
	struct trace_event_raw_neigh_update *ctx,
	struct in6_addr *address)
{
	__builtin_memcpy(
		address,
		ctx->primary_key6,
		sizeof(*address));
}


static __always_inline void split_address(
	const struct in6_addr *address,
	__u64 *addr_hi,
	__u64 *addr_lo)
{
	__builtin_memcpy(
		addr_hi,
		&address->s6_addr[0],
		sizeof(*addr_hi));

	__builtin_memcpy(
		addr_lo,
		&address->s6_addr[8],
		sizeof(*addr_lo));
}


static __always_inline void reset_nud_failures(
	const struct in6_addr *vm)
{
	__u32 zero = 0;

	bpf_map_update_elem(
		&nud_failures,
		vm,
		&zero,
		BPF_ANY);
}


static __always_inline __u32 increment_nud_failures(
	const struct in6_addr *vm)
{
	__u32 *count;
	__u32 new_count;

	count = bpf_map_lookup_elem(
		&nud_failures,
		vm);

	if (!count)
	{
		new_count = 1;

		bpf_map_update_elem(
			&nud_failures,
			vm,
			&new_count,
			BPF_ANY);

		return new_count;
	}

	new_count = *count + 1;

	bpf_map_update_elem(
		&nud_failures,
		vm,
		&new_count,
		BPF_ANY);

	return new_count;
}


static __always_inline void remove_remote_vm(
	const struct in6_addr *vm)
{
	struct config *local_config;
	__u64 addr_hi;
	__u64 addr_lo;

	local_config = get_config();

	if (!local_config)
		return;

	bpf_map_delete_elem(
		&remote_vms,
		vm);

	split_address(
		vm,
		&addr_hi,
		&addr_lo);

	atlas_delete_neigh(
		local_config->discovery_ifindex,
		addr_hi,
		addr_lo);

	bpf_map_delete_elem(
		&nud_failures,
		vm);
}


SEC("tracepoint/neigh/neigh_update")
int handle_atlas_nud(
	struct trace_event_raw_neigh_update *ctx)
{
	struct in6_addr vm = {};
	__u32 count;

	if (ctx->family != AF_INET6)
		return 0;

	get_nud_address(
		ctx,
		&vm);

	if (!bpf_map_lookup_elem(
		    &remote_vms,
		    &vm))
		return 0;

	/*
	 * The trace event carries the neighbour flags in one byte, so the
	 * managed bit at bit 8 is lost. The externally-learned bit at bit 4
	 * survives, and only Atlas registrations set it. The remote_vms
	 * lookup above already limits the set to Atlas remote VMs.
	 */
	if (!(ctx->flags & NTF_EXT_LEARNED))
		return 0;

	if (ctx->new_state == NUD_REACHABLE)
	{
		reset_nud_failures(&vm);

		return 0;
	}

	if (ctx->new_state != NUD_FAILED)
		return 0;

	count = increment_nud_failures(&vm);

	if (count < ATLAS_NUD_FAILURE_LIMIT)
		return 0;

	remove_remote_vm(&vm);

	return 0;
}

#endif /* ATLAS_NUD_HOOK_H */