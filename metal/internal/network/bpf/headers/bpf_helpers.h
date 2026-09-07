#ifndef ATLAS_BPF_HELPERS_H
#define ATLAS_BPF_HELPERS_H

#include "linux_bpf.h"

/* Place a symbol in a named ELF section and keep it. */
#define SEC(name) __attribute__((section(name), used))

/* BTF map definition fields. The layout matches libbpf. */
#define __uint(name, val) int (*name)[val]
#define __type(name, val) typeof(val) *name

/* BPF helper functions. The integer is the stable kernel helper ID. */
static __u64 (*bpf_ktime_get_ns)(void) = (void *)5;
static long (*bpf_map_update_elem)(void *map, const void *key, const void *value, __u64 flags) = (void *)2;

#endif /* ATLAS_BPF_HELPERS_H */
