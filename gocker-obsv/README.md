# gocker-obsv

`gocker-obsv` provides host-side observability for Gocker containers. It uses
eBPF tracepoints and cgroup v2 files to collect per-container CPU, scheduler,
memory, page-fault, pressure stall information (PSI), and system-call metrics.
The collector exposes these metrics in Prometheus format and includes a Grafana
dashboard for visualization.

## Host Setup

Run the following commands from the `gocker-obsv` directory.

### 1. Install Prerequisites

On Debian or Ubuntu:

```bash
sudo apt-get install -y clang llvm bpftool libbpf-dev make curl
```

The collector also requires Go, a Linux kernel with cgroup v2 and BTF support,
and sufficient privileges to load and attach eBPF programs.

### 2. Enable Scheduler Statistics

Enable `kernel.sched_schedstats` so that detailed scheduler runtime, wait, and
I/O-wait statistics are available to eBPF and interfaces such as
`/proc/schedstat`:

```bash
sudo sysctl kernel.sched_schedstats=1
```

### 3. Build and Start the Collector

Build the eBPF programs and Go binaries, then start the collector:

```bash
bash run.sh
```

Alternatively, after building the binaries, start the collector directly:

```bash
sudo PF_TARGET_CGROUP=/sys/fs/cgroup/gocker PF_SAMPLE_RATE=1 ./collector
```

The default target is the `/sys/fs/cgroup/gocker` cgroup subtree, and the
default sample rate is `1`, which collects every event.

### 4. Verify the Exporter

The collector exposes Prometheus metrics on port `2112`:

```bash
curl -s localhost:2112/metrics \
  | egrep 'page_faults_total|sched_events_total|syscall_.*_total' \
  | head
```

### 5. Configure Prometheus and Grafana

Use the included [`prometheus.yml`](prometheus.yml) to configure Prometheus to
scrape the collector at `localhost:2112`.

Start Grafana:

```bash
grafana-server web
```

Open <http://localhost:3000> and sign in with the default credentials
`admin`/`admin`. Then select **+ > Import**, choose
[`grafana-dashboard.json`](grafana-dashboard.json), and select the Prometheus
data source.

### 6. Update the Runtime Configuration (Optional)

For example, change the sample rate to `10` to collect one out of every ten
events per CPU:

```bash
curl -X POST \
  -H 'Content-Type: application/json' \
  -d '{"sample_rate":10,"enable_filter":1}' \
  http://127.0.0.1:2112/admin/config
```

The same setting can be applied with the CLI:

```bash
./ctl --sample 10
```

### 7. Find a Container's cgroup ID

The `cgroup_id` metric label is the inode number of the container's cgroup
directory. To inspect it, run:

```bash
stat -Lc '%i' /sys/fs/cgroup/gocker/container_name
```

## Exported Metrics

All metrics are available from the collector's `/metrics` endpoint.

### `page_faults_total{type,cgroup_id}`

- Source: `exceptions/page_fault_user` and `exceptions/page_fault_kernel`
  tracepoints via the eBPF `cg_pf_cnt` map.
- Type label: `user` or `kernel`.
- Metric type: counter (events).
- Example page-fault rate:

  ```promql
  rate(page_faults_total[30s])
  ```

### `sched_events_total{type,cgroup_id}`

- Source: `sched/sched_switch` and `sched/sched_wakeup` tracepoints via the
  `cg_sched_cnt` map.
- Type label: `switch` or `wakeup`.
- Metric type: counter (events).
- Example event rates:

  ```promql
  rate(sched_events_total{type="switch"}[30s])
  rate(sched_events_total{type="wakeup"}[30s])
  ```

- Example scheduler pressure ratio:

  ```promql
  sum by (cgroup_id) (rate(sched_events_total{type="wakeup"}[2m]))
  /
  clamp_min(
    sum by (cgroup_id) (rate(sched_events_total{type="switch"}[2m])),
    1e-9
  )
  ```

### `cpu_sched_ns_total{type,cgroup_id}`

- Source: `sched_stat_runtime`, `sched_stat_wait`, and `sched_stat_iowait`
  tracepoints via the `cg_cpu_ns` map.
- Type label: `runtime`, `wait`, or `iowait`.
- Unit: nanoseconds.
- Metric type: counter (accumulated time).
- Requires `kernel.sched_schedstats=1`.
- Example CPU utilization in cores:

  ```promql
  sum by (cgroup_id) (rate(cpu_sched_ns_total{type="runtime"}[1m])) / 1e9
  ```

- Example run-queue wait share:

  ```promql
  sum by (cgroup_id) (rate(cpu_sched_ns_total{type="wait"}[1m]))
  /
  sum by (cgroup_id) (rate(cpu_sched_ns_total{type=~"runtime|wait"}[1m]))
  ```

- Example I/O-wait share:

  ```promql
  sum by (cgroup_id) (rate(cpu_sched_ns_total{type="iowait"}[1m]))
  /
  sum by (cgroup_id) (rate(cpu_sched_ns_total{type=~"runtime|iowait"}[1m]))
  ```

### `memory_page_faults_total{type,cgroup_id}`

- Source: deltas from the cgroup v2 `memory.stat` values `pgfault` and
  `pgmajfault`.
- Type label: `major` or `minor`.
- Metric type: counter (events).
- Example:

  ```promql
  sum by (cgroup_id, type) (rate(memory_page_faults_total[1m]))
  ```

### PSI Stall Metrics

- `psi_cpu_stall_seconds_total{level,cgroup_id}`
- `psi_io_stall_seconds_total{level,cgroup_id}`
- `psi_memory_stall_seconds_total{level,cgroup_id}`

These counters are calculated from the `total` field in each cgroup v2
`<resource>.pressure` file. The source values are converted from microseconds
to seconds. The `level` label is either `some` or `full`.

Examples:

```promql
rate(psi_cpu_stall_seconds_total{level="some"}[5m])
rate(psi_io_stall_seconds_total{level="some"}[5m])
rate(psi_memory_stall_seconds_total{level="some"}[5m])
```

### `syscall_calls_total{syscall,cgroup_id}`

- Source: `raw_syscalls/sys_enter` and `raw_syscalls/sys_exit` tracepoints via
  the `cg_sys_cnt` map.
- Metric type: counter (calls).
- Example:

  ```promql
  sum by (cgroup_id, syscall) (rate(syscall_calls_total[30s]))
  ```

### `syscall_latency_nanoseconds_total{syscall,cgroup_id}`

- Source: accumulated time between `sys_enter` and `sys_exit` via the
  `cg_sys_lat_ns` map.
- Unit: nanoseconds.
- Metric type: counter (accumulated latency).
- Example average latency in nanoseconds:

  ```promql
  sum by (cgroup_id, syscall) (
    rate(syscall_latency_nanoseconds_total[30s])
  )
  /
  ignoring()
  sum by (cgroup_id, syscall) (rate(syscall_calls_total[30s]))
  ```

### `mm_vmscan_events_total{type,cgroup_id}`

- Source: `vmscan/mm_vmscan_direct_reclaim_begin` tracepoint via the
  `cg_mem_evt` map.
- The only current type is `direct_reclaim`, which counts how often a container
  performs direct memory reclaim while allocating memory.
- Example:

  ```promql
  sum by (cgroup_id) (
    rate(mm_vmscan_events_total{type="direct_reclaim"}[$__rate_interval])
  )
  ```

### `mm_vmscan_pages_total{type,cgroup_id}`

- Source: `vmscan/mm_vmscan_reclaim_pages` tracepoint via the `cg_mem_pages`
  map.
- The `reclaim_pages` type counts the pages reported as reclaimed during the
  reclaim process.
- Example:

  ```promql
  sum by (cgroup_id) (
    rate(mm_vmscan_pages_total{type="reclaim_pages"}[$__rate_interval])
  )
  ```

- Example reclaim efficiency:

  ```promql
  clamp_min(
    sum(rate(mm_vmscan_pages_total{type="reclaim_pages"}[$__rate_interval]))
    /
    sum(rate(mm_vmscan_events_total{type="direct_reclaim"}[$__rate_interval])),
    0
  )
  ```

### `mm_page_alloc_bytes_total{cgroup_id}` and `mm_page_free_bytes_total{cgroup_id}`

- Source: `kmem/mm_page_alloc` and `kmem/mm_page_free` tracepoints via the
  `cg_mem_bytes` map.
- These metrics count the base-page bytes allocated and freed through the buddy
  allocator for each container. Divide by `4096`, or by
  `node_memory_PageSize_bytes`, in PromQL to convert bytes per second to pages
  per second.
- Example allocation rate in pages per second:

  ```promql
  sum by (cgroup_id) (rate(mm_page_alloc_bytes_total[$__rate_interval])) / 4096
  ```

- Example free rate in pages per second:

  ```promql
  sum by (cgroup_id) (rate(mm_page_free_bytes_total[$__rate_interval])) / 4096
  ```

- Example net allocation rate in bytes per second:

  ```promql
  sum(rate(mm_page_alloc_bytes_total[$__rate_interval]))
  -
  sum(rate(mm_page_free_bytes_total[$__rate_interval]))
  ```

### `mm_vmscan_kswapd_wake_total`

- Source: `vmscan/mm_vmscan_kswapd_wake` tracepoint via the `kswapd_cnt` map.
- This is a system-wide counter of `kswapd` wakeups. A sustained increase can
  indicate system-wide memory pressure.
- Example:

  ```promql
  rate(mm_vmscan_kswapd_wake_total[$__rate_interval])
  ```

## Notes

- By default, per-cgroup metrics are limited to the
  `/sys/fs/cgroup/gocker` subtree. The target can be changed through
  `PF_TARGET_CGROUP` when the collector starts. The
  `mm_vmscan_kswapd_wake_total` metric is system-wide.
- The sample rate defaults to `1` and can be updated at runtime through
  `/admin/config` or the `ctl` command.
- For the Grafana `cgroup_id` variable, use
  `label_values(cpu_sched_ns_total, cgroup_id)` and set the **All** value to
  `.*`. This prevents containers without page faults from being omitted.
