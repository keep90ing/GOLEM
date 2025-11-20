// bpf/cpu.bpf.c
#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char LICENSE[] SEC("license") = "Dual BSD/GPL";

// .rodata
const volatile __u32 SAMPLE_RATE   = 1;  // 事件取樣率, ex : rate <= 1表示全收集，rate = 10表示每10個事件收集一次(per CPU)
const volatile __u32 ENABLE_FILTER = 1;  // 1 : 只取樣target subtree  , 0 : 全域
const volatile __u32 TARGET_LEVEL  = 1;  // 用bpf_get_current_ancestor_cgroup_id(level)取得向上第level層的cgroup id 
const volatile __u64 TARGET_CGID   = 0;  // 目標cgroup的ID（需與TARGET_LEVEL對應的層級一致）。只有當目前任務在該level的祖先cgroup ID等於此值時才通過過濾

// 定義可熱更新的配置結構 cfg，並用一個單元素的cfg_map 存放當前配置，讓 eBPF 程式在執行中調整行為（無需重載bpf程式）
struct cfg { __u32 sample_rate, enable_filter, target_level; __u64 target_cgid; };
struct { __uint(type, BPF_MAP_TYPE_ARRAY); __uint(max_entries, 1);
         __type(key, __u32); __type(value, struct cfg); } cfg_map SEC(".maps");

enum cpu_type { CPU_RUNTIME = 1, CPU_WAIT = 2, CPU_IOWAIT = 3 };
struct cg_key { __u64 cgid; __u32 type; __u32 pad; }; // padding（填充位元），為了讓整個struct alignment至 8 bytes。

// cg_cpu_ns 用於累加各 cgroup 的 runtime/wait/iowait（以nanosecond為單位）
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct cg_key);
    __type(value, __u64);    // 單位：ns（nanoseconds）
} cg_cpu_ns SEC(".maps");

// 記住pid → cgroup ID的對應
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);  //自動淘汰最近最少使用的鍵值
    __uint(max_entries, 65536);
    __type(key, __u32); // pid
    __type(value, __u64);  // cgroup id
} pid_cgid SEC(".maps");

// 取樣計數器，控制事件降採樣（每 CPU 各自計次）
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32); // key = 0 ,當 count % SAMPLE_RATE == 0 才放行
    __type(value, __u64);  
} per_cpu_cnt SEC(".maps");

// 說明：load_cfg 與 pass_sample 與其他模組共享邏輯，透過 cfg_map 調整熱更新參數

// 把目前的設定載入到暫存變數（rate/en/lvl/cgid），優先使用使用者態寫入的 cfg_map[0]，沒有時回退到 .rodata 常數。
static __always_inline void load_cfg(__u32 *rate, __u32 *en, __u32 *lvl, __u64 *cgid) {
    __u32 k = 0;
    struct cfg *c = bpf_map_lookup_elem(&cfg_map, &k);
    if (c) { *rate = c->sample_rate ? c->sample_rate : 1; *en = c->enable_filter; *lvl = c->target_level; *cgid = c->target_cgid; }
    else   { *rate = SAMPLE_RATE ? SAMPLE_RATE : 1;       *en = ENABLE_FILTER;     *lvl = TARGET_LEVEL;     *cgid = TARGET_CGID; }
}

// 判斷當前事件是否屬於「目標 cgroup 子樹」
static __always_inline bool in_target_subtree_current(void) {
    __u32 rate, en, lvl; __u64 cgid;
    load_cfg(&rate, &en, &lvl, &cgid);
    if (!en) { // closed filter
        return true;
    }
    __u64 anc = bpf_get_current_ancestor_cgroup_id((int)lvl); // 取得向上第lvl層的cgroup id 
    return anc && anc == cgid;
}

// 以每 CPU 的計數器做確定性降採樣，降低事件更新頻率
static __always_inline bool pass_sample(void) {
    __u32 rate, en, lvl; __u64 cgid;
    load_cfg(&rate, &en, &lvl, &cgid);
    __u32 k = 0;
    __u64 *pc = bpf_map_lookup_elem(&per_cpu_cnt, &k);
    if (!pc) {
        return true;
    }
    *pc += 1;
    if (rate <= 1) {
        return true;
    }
    return (*pc % rate) == 0;
}

// 把pid -> cgroup id 快取起來到pid_cgip ，方便之後查詢
static __always_inline void remember_pid(__u32 pid, __u64 cgid) {
    if (!pid || !cgid) {
        return;
    }
    bpf_map_update_elem(&pid_cgid, &pid, &cgid, BPF_ANY);
}

// 若快取map已存在 pid 對應的 cgroup id，可透過此函式回傳給後續計算使用
static __always_inline bool pid_to_cgid(__u32 pid, __u64 *cgid) {
    __u64 *val = bpf_map_lookup_elem(&pid_cgid, &pid);
    if (!val) {
        return false;
    }
    *cgid = *val;
    return true;
}

// 某個 cgid在特定type的使用時間（delta，單位 ns）累加到對應的 cg_cpu_ns map
static __always_inline int add_cpu_ns(__u64 cgid, __u32 type, __u64 delta) {
    if (!cgid || !delta) {
        return 0;
    }
    struct cg_key key = { .cgid = cgid, .type = type };
    __u64 *val = bpf_map_lookup_elem(&cg_cpu_ns, &key);
    if (!val) {
        __u64 zero = 0;  //若查不到這個 key，就插入一筆 {key → 0} 初始值
        bpf_map_update_elem(&cg_cpu_ns, &key, &zero, BPF_NOEXIST); //BPF_NOEXIST 代表只有當 key 不存在時才新增（避免覆蓋舊值）
        val = bpf_map_lookup_elem(&cg_cpu_ns, &key);  //插入後再查一次，確保能成功取得指標
        if (!val) {
            return 0;
        }
    }
    *val += delta;  
    return 0;
}

SEC("tracepoint/sched/sched_switch")
int tp_sched_switch_cpu(struct trace_event_raw_sched_switch *ctx)
{
    if (!in_target_subtree_current()) {
        return 0;
    }
    __u64 cur_cgid = bpf_get_current_cgroup_id(); //拿到prev的cgroup id 
    remember_pid((__u32)ctx->prev_pid, cur_cgid);  
    return 0;
}

// 某個 process 結束時，把它在 pid_cgid map 裡的紀錄刪掉，以防 map 中殘留無效資料。
SEC("tracepoint/sched/sched_process_exit")
int tp_sched_exit_cpu(struct trace_event_raw_sched_process_template *ctx)
{
    __u32 pid = bpf_get_current_pid_tgid();
    bpf_map_delete_elem(&pid_cgid, &pid);
    return 0;
}

// process剛於 CPU 上實際執行完成時觸發
SEC("tracepoint/sched/sched_stat_runtime")
int tp_sched_stat_runtime(struct trace_event_raw_sched_stat_runtime *ctx)
{
    __u32 pid = (__u32)ctx->pid;
    __u64 cgid = 0;
    if (in_target_subtree_current()) {
        cgid = bpf_get_current_cgroup_id();
        remember_pid(pid, cgid);
    } else {
        if (!pid_to_cgid(pid, &cgid)) {
            return 0;
        }
    }
    if (!pass_sample()) {
        return 0;
    }
    return add_cpu_ns(cgid, CPU_RUNTIME, ctx->runtime);  // ctx->runtime 代表這次在 CPU 上跑了多久（單位：ns）
}

// 說明：handle_delay_event 將 wait/iowait 延遲歸戶到對應 cgroup
static __always_inline int handle_delay_event(__u32 pid, __u64 delay, __u32 type)
{
    __u64 cgid = 0;
    if (!pid_to_cgid(pid, &cgid)) {
        return 0;
    }
    if (!pass_sample()) {
        return 0;
    }
    return add_cpu_ns(cgid, type, delay);
}

// process 在 runnable queue 等 CPU 的時間累積（非執行、非 I/O block）
SEC("tracepoint/sched/sched_stat_wait")
int tp_sched_stat_wait(struct trace_event_raw_sched_stat_template *ctx)
{
    return handle_delay_event((__u32)ctx->pid, ctx->delay, CPU_WAIT); // delay = 等待時長
}

// 任務因 I/O block 而等待的時間累積
SEC("tracepoint/sched/sched_stat_iowait")
int tp_sched_stat_iowait(struct trace_event_raw_sched_stat_template *ctx)
{
    return handle_delay_event((__u32)ctx->pid, ctx->delay, CPU_IOWAIT); // delay = 等待時長（ns）
}
