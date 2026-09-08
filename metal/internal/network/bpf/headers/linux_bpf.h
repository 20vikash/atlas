#ifndef ATLAS_LINUX_BPF_H
#define ATLAS_LINUX_BPF_H

typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;
typedef unsigned long long __u64;

/* BPF map types used by this module. */
#define BPF_MAP_TYPE_HASH 1
#define BPF_MAP_TYPE_RINGBUF 27

/* Flags for bpf_map_update_elem. */
#define BPF_ANY 0
#define BPF_NOEXIST 1
#define BPF_EXIST 2

/* TCX return actions. TCX_NEXT lets the packet continue unchanged. */
#define TCX_NEXT (-1)
#define TCX_PASS 0
#define TCX_DROP 2

/* The TCX program context. The activity program does not read packet bytes, so
   the type stays opaque. */
struct __sk_buff;

#endif /* ATLAS_LINUX_BPF_H */
