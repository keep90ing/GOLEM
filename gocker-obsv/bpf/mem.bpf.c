// bpf/mem.bpf.c
#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#ifndef PAGE_SHIFT
#define PAGE_SHIFT 12
#endif

char LICENSE[] SEC("license") = "Dual BSD/GPL";

const volatile __u32 SAMPLE_RATE   = 1;
const volatile __u32 ENABLE_FILTER = 1;
const volatile __u32 TARGET_LEVEL  = 1;
const volatile __u64 TARGET_CGID   = 0;

struct cfg { __u32 sample_rate, enable_filter, target_level; __u64 target_cgid; };
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct cfg);
} cfg_map SEC(".maps");

enum mem_evt_type { MEM_EVT_DIRECT = 1 };
enum mem_page_type { MEM_PAGES_RECLAIM = 1 };
enum mem_bytes_type { MEM_BYTES_ALLOC = 1, MEM_BYTES_FREE = 2 };

struct cg_key { __u64 cgid; __u32 type; __u32 pad; };

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct cg_key);
    __type(value, __u64);
} cg_mem_evt SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct cg_key);
    __type(value, __u64);
} cg_mem_pages SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct cg_key);
    __type(value, __u64);
} cg_mem_bytes SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} per_cpu_cnt SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} kswapd_cnt SEC(".maps");

static __always_inline void load_cfg(__u32 *rate, __u32 *en, __u32 *lvl, __u64 *cgid)
{
    __u32 k = 0;
    struct cfg *c = bpf_map_lookup_elem(&cfg_map, &k);
    if (c) {
        *rate = c->sample_rate ? c->sample_rate : 1;
        *en = c->enable_filter;
        *lvl = c->target_level;
        *cgid = c->target_cgid;
    } else {
        *rate = SAMPLE_RATE ? SAMPLE_RATE : 1;
        *en = ENABLE_FILTER;
        *lvl = TARGET_LEVEL;
        *cgid = TARGET_CGID;
    }
}

static __always_inline bool in_target_subtree_current(void)
{
    __u32 rate, en, lvl;
    __u64 cgid;
    load_cfg(&rate, &en, &lvl, &cgid);
    if (!en) {
        return true;
    }
    __u64 anc = bpf_get_current_ancestor_cgroup_id((int)lvl);
    return anc && anc == cgid;
}

static __always_inline bool pass_sample(void)
{
    __u32 rate, en, lvl;
    __u64 cgid;
    load_cfg(&rate, &en, &lvl, &cgid);
    if (rate <= 1) {
        return true;
    }
    __u32 k = 0;
    __u64 *pc = bpf_map_lookup_elem(&per_cpu_cnt, &k);
    if (!pc) {
        return true;
    }
    *pc += 1;
    return (*pc % rate) == 0;
}

static __always_inline int add_cg_u64(struct bpf_map *m, __u64 cgid, __u32 type, __u64 delta)
{
    if (!cgid || !delta) {
        return 0;
    }
    struct cg_key key = { .cgid = cgid, .type = type };
    __u64 *val = bpf_map_lookup_elem(m, &key);
    if (!val) {
        __u64 zero = 0;
        bpf_map_update_elem(m, &key, &zero, BPF_NOEXIST);
        val = bpf_map_lookup_elem(m, &key);
        if (!val) {
            return 0;
        }
    }
    *val += delta;
    return 0;
}

static __always_inline int inc_kswapd(void)
{
    __u32 k = 0;
    __u64 *val = bpf_map_lookup_elem(&kswapd_cnt, &k);
    if (!val) {
        return 0;
    }
    *val += 1;
    return 0;
}

static __always_inline __u64 order_to_bytes(unsigned int order)
{
    if (order >= 32) {
        order = 31;
    }
    __u64 pages = 1ULL << order;
    return pages << PAGE_SHIFT;
}

SEC("tracepoint/vmscan/mm_vmscan_kswapd_wake")
int tp_mm_vmscan_kswapd_wake(struct trace_event_raw_mm_vmscan_kswapd_wake *ctx)
{
    if (!pass_sample()) {
        return 0;
    }
    return inc_kswapd();
}

SEC("tracepoint/vmscan/mm_vmscan_direct_reclaim_begin")
int tp_mm_vmscan_direct_reclaim_begin(struct trace_event_raw_mm_vmscan_direct_reclaim_begin_template *ctx)
{
    if (!in_target_subtree_current()) {
        return 0;
    }
    if (!pass_sample()) {
        return 0;
    }
    __u64 cgid = bpf_get_current_cgroup_id();
    return add_cg_u64((struct bpf_map *)&cg_mem_evt, cgid, MEM_EVT_DIRECT, 1);
}

SEC("tracepoint/vmscan/mm_vmscan_reclaim_pages")
int tp_mm_vmscan_reclaim_pages(struct trace_event_raw_mm_vmscan_reclaim_pages *ctx)
{
    if (!in_target_subtree_current()) {
        return 0;
    }
    if (!pass_sample()) {
        return 0;
    }
    __u64 cgid = bpf_get_current_cgroup_id();
    __u64 reclaimed = ctx->nr_reclaimed;
    return add_cg_u64((struct bpf_map *)&cg_mem_pages, cgid, MEM_PAGES_RECLAIM, reclaimed);
}

SEC("tracepoint/kmem/mm_page_alloc")
int tp_mm_page_alloc(struct trace_event_raw_mm_page_alloc *ctx)
{
    if (!in_target_subtree_current()) {
        return 0;
    }
    if (!pass_sample()) {
        return 0;
    }
    __u64 cgid = bpf_get_current_cgroup_id();
    __u64 bytes = order_to_bytes(ctx->order);
    if (!bytes) {
        return 0;
    }
    return add_cg_u64((struct bpf_map *)&cg_mem_bytes, cgid, MEM_BYTES_ALLOC, bytes);
}

SEC("tracepoint/kmem/mm_page_free")
int tp_mm_page_free(struct trace_event_raw_mm_page_free *ctx)
{
    if (!in_target_subtree_current()) {
        return 0;
    }
    if (!pass_sample()) {
        return 0;
    }
    __u64 cgid = bpf_get_current_cgroup_id();
    __u64 bytes = order_to_bytes(ctx->order);
    if (!bytes) {
        return 0;
    }
    return add_cg_u64((struct bpf_map *)&cg_mem_bytes, cgid, MEM_BYTES_FREE, bytes);
}
