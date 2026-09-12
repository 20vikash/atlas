// SPDX-License-Identifier: AGPL-3.0
//
// Atlas neighbour kfunc module.
//
// BPF cannot call the Linux neighbour table directly. This module exposes one
// kfunc that registers an IPv6 neighbour as externally learned and managed.
// TC BPF calls it when an Atlas NDP advertisement is observed. Linux NDP keeps
// full responsibility for the neighbour state and liveness after that.

#include <linux/bpf.h>
#include <linux/btf.h>
#include <linux/btf_ids.h>
#include <linux/init.h>
#include <linux/module.h>
#include <linux/netdevice.h>

#include <net/ipv6.h>
#include <net/ndisc.h>
#include <net/neighbour.h>

// Older kernels name the kfunc set macros BTF_SET8_* and do not mark kfunc
// declarations. Provide the newer names when they are missing.
#ifndef __bpf_kfunc
#define __bpf_kfunc
#endif
#ifndef BTF_KFUNCS_START
#define BTF_KFUNCS_START(name) BTF_SET8_START(name)
#endif
#ifndef BTF_KFUNCS_END
#define BTF_KFUNCS_END(name) BTF_SET8_END(name)
#endif

// Register one IPv6 neighbour on the given interface. The four address words
// are the in6_addr s6_addr32 fields of the address, passed through unchanged.
// The neighbour is created when it does not exist, and it is marked as
// externally learned and managed. Linux NUD then probes the entry and keeps it.
int atlas_register_neigh(__u32 ifindex, __u32 addr0, __u32 addr1,
			 __u32 addr2, __u32 addr3);
__bpf_kfunc int atlas_register_neigh(__u32 ifindex, __u32 addr0, __u32 addr1,
				     __u32 addr2, __u32 addr3)
{
	struct net_device *device;
	struct neighbour *neighbour;
	struct in6_addr address;
	int result;

	address.s6_addr32[0] = addr0;
	address.s6_addr32[1] = addr1;
	address.s6_addr32[2] = addr2;
	address.s6_addr32[3] = addr3;

	// Atlas hosts run in the initial network namespace.
	device = dev_get_by_index(&init_net, ifindex);
	if (!device)
		return -ENODEV;

	neighbour = neigh_lookup(&nd_tbl, &address, device);
	if (!neighbour) {
		neighbour = __neigh_create(&nd_tbl, &address, device, true);
		if (IS_ERR(neighbour)) {
			result = PTR_ERR(neighbour);
			dev_put(device);
			return result;
		}
	}

	// An externally learned entry is not removed by normal neighbour garbage
	// collection, because an external controller owns it.
	result = neigh_update(neighbour, NULL, NUD_STALE,
			      NEIGH_UPDATE_F_ADMIN |
				      NEIGH_UPDATE_F_EXT_LEARNED,
			      0);
	if (result)
		goto out;

	// A managed entry is resolved and probed by the kernel neighbour
	// subsystem on its own schedule.
	result = neigh_update(neighbour, NULL, NUD_STALE,
			      NEIGH_UPDATE_F_ADMIN |
				      NEIGH_UPDATE_F_EXT_LEARNED |
				      NEIGH_UPDATE_F_MANAGED,
			      0);

out:
	neigh_release(neighbour);
	dev_put(device);
	return result;
}

BTF_KFUNCS_START(atlas_neigh_kfunc_ids)
BTF_ID_FLAGS(func, atlas_register_neigh)
BTF_KFUNCS_END(atlas_neigh_kfunc_ids)

static const struct btf_kfunc_id_set atlas_neigh_kfunc_set = {
	.owner = THIS_MODULE,
	.set = &atlas_neigh_kfunc_ids,
};

static int __init atlas_neigh_init(void)
{
	int result;

	result = register_btf_kfunc_id_set(BPF_PROG_TYPE_SCHED_CLS,
					   &atlas_neigh_kfunc_set);
	if (result) {
		pr_err("atlas_neigh: kfunc registration failed: %d\n", result);
		return result;
	}

	pr_info("atlas_neigh: kfunc registered\n");
	return 0;
}

module_init(atlas_neigh_init);

MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Atlas neighbour management kfunc");
MODULE_AUTHOR("Atlas");
