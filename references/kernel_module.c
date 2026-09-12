#include <linux/module.h>
#include <linux/init.h>
#include <linux/bpf.h>
#include <linux/btf.h>
#include <linux/btf_ids.h>
#include <linux/netdevice.h>

#include <net/neighbour.h>
#include <net/ndisc.h>
#include <net/ipv6.h>


/*
 * Register an IPv6 neighbour as:
 *
 *     MANAGED
 *     EXT_LEARNED
 *
 * Called from TC BPF when an NDP Neighbor Advertisement
 * is observed.
 *
 * The normal Linux NDP code is still responsible for
 * processing the NA and learning the link-layer address.
 */
__bpf_kfunc int atlas_register_neigh(
    __u32 ifindex,
    __u32 addr0,
    __u32 addr1,
    __u32 addr2,
    __u32 addr3
)
{
    struct net_device *dev;
    struct neighbour *neigh;
    struct in6_addr addr;
    int ret;


    /*
     * Reconstruct the IPv6 address.
     */
    addr.s6_addr32[0] = addr0;
    addr.s6_addr32[1] = addr1;
    addr.s6_addr32[2] = addr2;
    addr.s6_addr32[3] = addr3;


    /*
     * POC:
     * Use the initial network namespace.
     */
    dev = dev_get_by_index(&init_net, ifindex);

    if (!dev)
        return -ENODEV;


    /*
     * Look for an existing neighbour.
     */
    neigh = neigh_lookup(
        &nd_tbl,
        &addr,
        dev
    );


    /*
     * If the entry doesn't exist, create it.
     *
     * The kernel's IPv6 neighbour table constructor
     * creates the appropriate neighbour object.
     */
    if (!neigh) {
        neigh = __neigh_create(
            &nd_tbl,
            &addr,
            dev,
            true
        );

        if (IS_ERR(neigh)) {
            ret = PTR_ERR(neigh);
            dev_put(dev);
            return ret;
        }
    }


    /*
     * Mark the entry as externally learned.
     *
     * This prevents normal neighbour GC from removing
     * the dynamic entry.
     */
    ret = neigh_update(
        neigh,
        NULL,
        NUD_STALE,
        NEIGH_UPDATE_F_ADMIN |
        NEIGH_UPDATE_F_EXT_LEARNED,
        0
    );

    if (ret)
        goto out;


    /*
     * Mark the entry as managed.
     *
     * This tells the kernel neighbour subsystem to
     * maintain/refresh the neighbour.
     */
    ret = neigh_update(
        neigh,
        NULL,
        NUD_STALE,
        NEIGH_UPDATE_F_ADMIN |
        NEIGH_UPDATE_F_EXT_LEARNED |
        NEIGH_UPDATE_F_MANAGED,
        0
    );


out:
    neigh_release(neigh);
    dev_put(dev);

    return ret;
}


/*
 * BTF kfunc registration.
 */
BTF_KFUNCS_START(atlas_neigh_ids)

BTF_ID_FLAGS(
    func,
    atlas_register_neigh
)

BTF_KFUNCS_END(atlas_neigh_ids)


static const struct btf_kfunc_id_set atlas_neigh_set = {
    .owner = THIS_MODULE,
    .set = &atlas_neigh_ids,
};


/*
 * Module initialization.
 */
static int __init atlas_neigh_init(void)
{
    int ret;

    pr_info("ATLAS NEIGH: loading module\n");

    ret = register_btf_kfunc_id_set(
        BPF_PROG_TYPE_SCHED_CLS,
        &atlas_neigh_set
    );

    if (ret) {
        pr_err(
            "ATLAS NEIGH: registration failed: %d\n",
            ret
        );

        return ret;
    }

    pr_info(
        "ATLAS NEIGH: registered successfully\n"
    );

    return 0;
}


module_init(atlas_neigh_init);


MODULE_LICENSE("GPL");
MODULE_AUTHOR("Atlas");
MODULE_DESCRIPTION(
    "Atlas neighbour management kfunc"
);
