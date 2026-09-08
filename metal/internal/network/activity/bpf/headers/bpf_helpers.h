#ifndef ATLAS_BPF_HELPERS_H
#define ATLAS_BPF_HELPERS_H

#include "linux_bpf.h"

/* Place a symbol in a named ELF section and keep it. */
#define SEC(name) __attribute__((section(name), used))

/* Force a helper to be inlined into its caller. */
#define __always_inline inline __attribute__((always_inline))

/* BTF map definition fields. The layout matches libbpf. */
#define __uint(name, val) int (*name)[val]
#define __type(name, val) typeof(val) *name

/* BPF helper functions. The integer is the stable kernel helper ID. */
static __u64 (*bpf_ktime_get_ns)(void) = (void *)5;
static long (*bpf_map_update_elem)(void *map, const void *key, const void *value, __u64 flags) = (void *)2;
static long (*bpf_skb_load_bytes)(const void *skb, __u32 offset, void *to, __u32 len) = (void *)26;
static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;
static void *(*bpf_ringbuf_reserve)(void *ringbuf, __u64 size, __u64 flags) = (void *)131;
static void (*bpf_ringbuf_submit)(void *data, __u64 flags) = (void *)132;
static void (*bpf_ringbuf_discard)(void *data, __u64 flags) = (void *)133;

#endif /* ATLAS_BPF_HELPERS_H */
