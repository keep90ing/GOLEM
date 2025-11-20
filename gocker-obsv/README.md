# 操作步驟（Host-only）
A. 安裝先決套件（以 Debian/Ubuntu 為例）
```
sudo apt-get install -y clang llvm bpftool curl
```

B. 開啟 Linux 核心的排程統計功能（sched_schedstats），才可以在eBPF 或 /proc/schedstat 之類的介面中讀到詳細的 scheduler runtime/wait/iowait 統計。
```
sudo sysctl kernel.sched_schedstats=1
```
C. 啟動
```
sudo PF_TARGET_CGROUP=/sys/fs/cgroup/gocker PF_SAMPLE_RATE=1 ./collector
```
or
```
bash run.sh
```

D. 驗證 Exporter
```
curl -s localhost:2112/metrics | egrep 'page_faults_total|sched_events_total|syscall_.*_total' | head
```

E. 啟動 Grafana 並匯入 Dashboard
grafana-server web
瀏覽 http://localhost:3000（預設帳密 admin/admin）
 → “+” → Import → 選 grafana-dashboard.json → 選取「Prometheus」資料源

F. 熱更新參數（可選）
*  把 sample_rate 改 10（降低採計事件量）
```
curl -XPOST -H 'Content-Type: application/json' \
  -d '{"sample_rate":10}' http://127.0.0.1:2112/admin/config
```
或用 CLI
```
./ctl --sample 10
```

G. 備註
查看container 的 cgroup id
```
stat -Lc '%i' /sys/fs/cgroup/gocker/container_name 
```

## Metrics 說明（Exporter /metrics）

- page_faults_total{type,cgroup_id}
  - 來源：tracepoint exceptions/page_fault_user|page_fault_kernel（eBPF map `cg_pf_cnt`）
  - type：user｜kernel；性質：Counter（事件數）
  - 常用：缺頁率 `rate(page_faults_total[30s])`

- sched_events_total{type,cgroup_id}
  - 來源：tracepoint sched/sched_switch（switch）與 sched/sched_wakeup（wakeup）（map `cg_sched_cnt`）
  - 性質：Counter；常用：
    - `rate(sched_events_total{type="switch"}[30s])`、`rate(...{type="wakeup"}[30s])`
    - Scheduler Pressure：`sum by (cgroup_id)(rate(...{type="wakeup"}[2m])) / clamp_min(sum by (cgroup_id)(rate(...{type="switch"}[2m])),1e-9)`

- cpu_sched_ns_total{type,cgroup_id}
  - 來源：tracepoint sched_stat_runtime｜sched_stat_wait｜sched_stat_iowait（map `cg_cpu_ns`），單位 ns；需 `kernel.sched_schedstats=1`
  - type：runtime｜wait｜iowait；性質：Counter（時間累積）
  - 常用：
    - CPU 利用率（cores）：`sum by (cgroup_id)(rate(cpu_sched_ns_total{type="runtime"}[1m])) / 1e9`
    - Run-queue Wait Share：`sum by (cgroup_id)(rate(cpu_sched_ns_total{type="wait"}[1m])) / sum by (cgroup_id)(rate(cpu_sched_ns_total{type=~"runtime|wait"}[1m]))`
    - I/O Wait Share：`sum by (cgroup_id)(rate(cpu_sched_ns_total{type="iowait"}[1m])) / sum by (cgroup_id)(rate(cpu_sched_ns_total{type=~"runtime|iowait"}[1m]))`

- memory_page_faults_total{type,cgroup_id}
  - 來源：cgroup v2 `memory.stat`（`pgfault`、`pgmajfault` 差分），type：major｜minor；性質：Counter（事件數）
  - 常用：`sum by (cgroup_id,type)(rate(memory_page_faults_total[1m]))`

- psi_cpu_stall_seconds_total / psi_io_stall_seconds_total / psi_memory_stall_seconds_total{level,cgroup_id}
  - 來源：cgroup v2 `<resource>.pressure` 檔案的 `total`（微秒差分）；level：some｜full；性質：Counter（秒）
  - 常用：
    - CPU：`rate(psi_cpu_stall_seconds_total{level="some"}[5m])`
    - IO：`rate(psi_io_stall_seconds_total{level="some"}[5m])`
    - Memory：`rate(psi_memory_stall_seconds_total{level="some"}[5m])`

- syscall_calls_total{syscall,cgroup_id}
  - 來源：raw_syscalls/sys_enter|sys_exit（map `cg_sys_cnt`）；性質：Counter（呼叫次數）
  - 常用：`sum by (cgroup_id,syscall)(rate(syscall_calls_total[30s]))`

- syscall_latency_nanoseconds_total{syscall,cgroup_id}
  - 來源：enter/exit 間加總延遲（map `cg_sys_lat_ns`）；性質：Counter（ns）
  - 平均延遲（ns）：`(sum by (cgroup_id,syscall)(rate(syscall_latency_nanoseconds_total[30s]))) / ignoring() (sum by (cgroup_id,syscall)(rate(syscall_calls_total[30s])))`

- mm_vmscan_events_total{type,cgroup_id}
  - 來源：tracepoint `vmscan/mm_vmscan_direct_reclaim_begin`（map `cg_mem_evt`）；目前 type 僅 `direct_reclaim`，表示該容器為了配置記憶體而進行直接回收的次數
  - 常用：`sum by (cgroup_id)(rate(mm_vmscan_events_total{type="direct_reclaim"}[$__rate_interval]))`

- mm_vmscan_pages_total{type,cgroup_id}
  - 來源：tracepoint `vmscan/mm_vmscan_reclaim_pages`（map `cg_mem_pages`）；`type="reclaim_pages"` 代表在回收流程中掃到的頁數
  - 常用：`sum by (cgroup_id)(rate(mm_vmscan_pages_total{type="reclaim_pages"}[$__rate_interval]))`
  - 回收效率：`clamp_min( sum(rate(mm_vmscan_pages_total{type="reclaim_pages"}[$__rate_interval])) / sum(rate(mm_vmscan_events_total{type="direct_reclaim"}[$__rate_interval])), 0 )`

- mm_page_alloc_bytes_total{cgroup_id} / mm_page_free_bytes_total{cgroup_id}
  - 來源：tracepoint `kmem/mm_page_alloc`、`kmem/mm_page_free`；order 轉成 bytes 後依 cgroup 累計（map `cg_mem_bytes`）
  - 代表該容器透過 buddy allocator 配置 / 釋放的 base page bytes；視圖表需求可在 PromQL 中除以 4096（或 node_memory_PageSize_bytes）換算成 pages/s
  - 範例：
    - Allocation pages/s：`sum by (cgroup_id)(rate(mm_page_alloc_bytes_total[$__rate_interval])) / 4096`
    - Free pages/s：`sum by (cgroup_id)(rate(mm_page_free_bytes_total[$__rate_interval])) / 4096`
    - Net allocation（bytes/s）：`sum(rate(mm_page_alloc_bytes_total[$__rate_interval])) - sum(rate(mm_page_free_bytes_total[$__rate_interval]))`

- mm_vmscan_kswapd_wake_total
  - 來源：tracepoint `vmscan/mm_vmscan_kswapd_wake`（map `kswapd_cnt`）；為全系統 Counter，表示 kswapd 被喚醒的次數
  - 常用：`rate(mm_vmscan_kswapd_wake_total[$__rate_interval])`；若值持續升高表示系統整體記憶體壓力大

附註與建議：
- 只計 `/sys/fs/cgroup/gocker` 子樹（collector 內建白名單），可用 `/admin/config` 調整目標。
- `sample_rate` 可動態調整以控制事件量（預設 1）。
- Grafana 變數 `cgroup_id` 建議使用 `label_values(cpu_sched_ns_total, cgroup_id)`，並把 All 值設為 `.*`，避免因無 page faults 而被過濾掉。
