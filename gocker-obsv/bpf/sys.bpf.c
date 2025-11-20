// bpf/sys.bpf.c
#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char LICENSE[] SEC("license") = "Dual BSD/GPL";

const volatile __u32 SAMPLE_RATE   = 1;
const volatile __u32 ENABLE_FILTER = 1;
const volatile __u32 TARGET_LEVEL  = 1;
const volatile __u64 TARGET_CGID   = 0;

struct cfg { __u32 sample_rate, enable_filter, target_level; __u64 target_cgid; };
struct { __uint(type, BPF_MAP_TYPE_ARRAY); __uint(max_entries, 1);
         __type(key, __u32); __type(value, struct cfg); } cfg_map SEC(".maps");

struct cg_key { __u64 cgid; __u32 type; __u32 pad; };

// 每 cgroup × 每 syscall 的「呼叫次數」累加器（Counter）
struct { __uint(type, BPF_MAP_TYPE_PERCPU_HASH); __uint(max_entries, 16384);
         __type(key, struct cg_key); __type(value, __u64); } cg_sys_cnt SEC(".maps");

// 每 cgroup × 每 syscall 的「總延遲（nanosecond）」累加器（Counter）
struct { __uint(type, BPF_MAP_TYPE_PERCPU_HASH); __uint(max_entries, 16384);
         __type(key, struct cg_key); __type(value, __u64); } cg_sys_lat_ns SEC(".maps");

struct start_key { __u32 pid; __u32 sys; };
struct start_val { __u64 ts_ns; __u64 cgid; };

// 說明：sys 模組在 sys_enter 記錄開始時間與 cgroup id，sys_exit 計算耗時後累加
struct { __uint(type, BPF_MAP_TYPE_LRU_HASH); __uint(max_entries, 65536);
         __type(key, struct start_key); __type(value, struct start_val); } sys_enter_start SEC(".maps");

// 把目前的設定載入到暫存變數（rate/en/lvl/cgid），優先使用使用者態寫入的 cfg_map[0]，沒有時回退到 .rodata 常數。
static __always_inline void load_cfg(__u32 *rate, __u32 *en, __u32 *lvl, __u64 *cgid) {
    __u32 k=0; struct cfg *c=bpf_map_lookup_elem(&cfg_map,&k);
    if (c){ *rate=c->sample_rate?c->sample_rate:1; *en=c->enable_filter; *lvl=c->target_level; *cgid=c->target_cgid; }
    else { *rate=SAMPLE_RATE?SAMPLE_RATE:1; *en=ENABLE_FILTER; *lvl=TARGET_LEVEL; *cgid=TARGET_CGID; }
}
// 在每次進入系統呼叫時記錄「開始時間與所屬 cgroup」，供對應的 sys_exit 計算延遲與累計次數
SEC("tracepoint/raw_syscalls/sys_enter")
int tp_sys_enter(struct trace_event_raw_sys_enter* ctx)
{
    __u32 rate,en,lvl; __u64 cgid; load_cfg(&rate,&en,&lvl,&cgid);
    if (en) {
        // 說明：若啟用過濾，只處理目標子樹內的系統呼叫
        __u64 anc = bpf_get_current_ancestor_cgroup_id((int)lvl);
        if (!(anc && anc == cgid)) return 0;
    }
    struct start_key k = { .pid = bpf_get_current_pid_tgid(), .sys = (__u32)ctx->id };
    struct start_val v = { .ts_ns = bpf_ktime_get_ns(), .cgid = bpf_get_current_cgroup_id() };
    bpf_map_update_elem(&sys_enter_start, &k, &v, BPF_ANY);
    return 0;
}

// 說明：add_u64() 在 map 中累計次數或延遲，若尚未存在會先初始化
static __always_inline void add_u64(struct bpf_map *m, struct cg_key *k, __u64 delta)
{
    __u64 *val = bpf_map_lookup_elem(m, k);
    if (!val) { __u64 z=0; bpf_map_update_elem(m, k, &z, BPF_NOEXIST);
                val=bpf_map_lookup_elem(m,k); if(!val) return; }
    *val += delta;
}

// sys_exit 會用相同 key {pid,sys} 查回 start_val，算 now - ts_ns 得出延遲，並以 {cgid, type=sys} 聚合
SEC("tracepoint/raw_syscalls/sys_exit")
int tp_sys_exit(struct trace_event_raw_sys_exit* ctx)
{
    struct start_key k = { .pid = bpf_get_current_pid_tgid(), .sys = (__u32)ctx->id };
    struct start_val *sv = bpf_map_lookup_elem(&sys_enter_start, &k);
    if (!sv) return 0;

    __u64 now = bpf_ktime_get_ns();  // 取得 單調遞增（monotonic）、以ns為單位的kernel時間
    __u64 dt  = now > sv->ts_ns ? now - sv->ts_ns : 0;  

    struct cg_key agg = { .cgid = sv->cgid, .type = k.sys };
    add_u64((struct bpf_map*)&cg_sys_cnt,    &agg, 1);  // 累加 (cgroup id, syscall type) 之 count 
    add_u64((struct bpf_map*)&cg_sys_lat_ns, &agg, dt); // 累加 (cgroup id, syscall type) 之 latency

    bpf_map_delete_elem(&sys_enter_start, &k);
    return 0;
}
