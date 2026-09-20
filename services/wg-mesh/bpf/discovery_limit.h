/* SPDX-License-Identifier: AGPL-3.0 */
/* Per-VM discovery rate limiting. */
#ifndef ATLAS_DISCOVERY_LIMIT_H
#define ATLAS_DISCOVERY_LIMIT_H

#include "state.h"

/* Sustained discovery probes per second, per VM. */
#define DISCOVERY_RATE_PER_SEC 10
/* Discovery bucket capacity per VM. */
#define DISCOVERY_BURST 50

struct discovery_limit
{
	__u64 last_ns;
	__s64 tokens_milli;
};

/* Only registered local VMs reach discovery, so this map is locally bounded. */
struct
{
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__type(key, struct in6_addr);
	__type(value, struct discovery_limit);
	__uint(max_entries, 4096);
} discovery_limits SEC(".maps");

/* Returns 1 when permitted, 0 when exhausted. */
static __always_inline int discovery_allowed(const struct in6_addr *virtual_machine)
{
	__u64 now = bpf_ktime_get_ns();
	struct discovery_limit *limit;
	__s64 cap, tokens;
	__u64 elapsed_ns, full_refill_ns;

	cap = (__s64)DISCOVERY_BURST * 1000;
	limit = bpf_map_lookup_elem(&discovery_limits, virtual_machine);
	if (!limit)
	{
		struct discovery_limit fresh = {
			.last_ns = now,
			.tokens_milli = cap - 1000,
		};
		bpf_map_update_elem(&discovery_limits, virtual_machine, &fresh, BPF_ANY);
		return 1;
	}

	/* Cap elapsed time before multiplication so a long idle period cannot overflow. */
	elapsed_ns = now - limit->last_ns;
	full_refill_ns = (__u64)DISCOVERY_BURST * 1000000000ULL / DISCOVERY_RATE_PER_SEC;
	if (elapsed_ns >= full_refill_ns)
		tokens = cap;
	else
		tokens = limit->tokens_milli + (__s64)(elapsed_ns * DISCOVERY_RATE_PER_SEC / 1000000ULL);
	limit->last_ns = now;

	if (tokens < 1000)
	{
		limit->tokens_milli = tokens;
		return 0;
	}
	limit->tokens_milli = tokens - 1000;
	return 1;
}

#endif /* ATLAS_DISCOVERY_LIMIT_H */
