// Go 1.22+ ; cilium/ebpf v0.15+
// 需 root 或 CAP_BPF+CAP_PERFMON+CAP_SYS_ADMIN
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	cgroupRootDefault = "/sys/fs/cgroup"
	gockerPathDefault = "/sys/fs/cgroup/gocker"
)

type cgKey struct {
	Cgid uint64
	Type uint32
	Pad  uint32
}

type cfg struct {
	SampleRate   uint32 `json:"sample_rate"`
	EnableFilter uint32 `json:"enable_filter"`
	TargetLevel  uint32 `json:"target_level"`
	TargetCgid   uint64 `json:"target_cgid"`
}

// 說明：memCounters/psiCounters 用於保存前次讀取值，方便計算差分
type memCounters struct {
	Major uint64
	Minor uint64
}

type psiCounters struct {
	Some uint64
	Full uint64
}

// Prom metrics
var (
	pageFaults = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "page_faults_total", Help: "Per-cgroup page faults."},
		[]string{"type", "cgroup_id"},
	)
	schedEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "sched_events_total", Help: "Per-cgroup scheduler events."},
		[]string{"type", "cgroup_id"},
	)
	sysCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "syscall_calls_total", Help: "Per-cgroup per-syscall calls."},
		[]string{"syscall", "cgroup_id"},
	)
	sysLatency = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "syscall_latency_nanoseconds_total", Help: "Per-cgroup per-syscall total latency (ns)."},
		[]string{"syscall", "cgroup_id"},
	)
	cpuSched = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "cpu_sched_ns_total", Help: "Per-cgroup scheduler time buckets (ns)."},
		[]string{"type", "cgroup_id"},
	)
	memEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "mm_vmscan_events_total", Help: "Per-cgroup vmscan events."},
		[]string{"type", "cgroup_id"},
	)
	memPages = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "mm_vmscan_pages_total", Help: "Per-cgroup reclaimed pages."},
		[]string{"type", "cgroup_id"},
	)
	pageAllocBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "mm_page_alloc_bytes_total", Help: "Per-cgroup page allocation bytes."},
		[]string{"cgroup_id"},
	)
	pageFreeBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "mm_page_free_bytes_total", Help: "Per-cgroup page free bytes."},
		[]string{"cgroup_id"},
	)
	kswapdWakeups = prometheus.NewCounter(
		prometheus.CounterOpts{Name: "mm_vmscan_kswapd_wake_total", Help: "System-wide kswapd wakeups."},
	)
	memoryFaults = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "memory_page_faults_total", Help: "Per-cgroup major/minor page faults from memory.stat."},
		[]string{"type", "cgroup_id"},
	)
	psiCPU = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "psi_cpu_stall_seconds_total", Help: "Per-cgroup CPU pressure stall time (seconds)."},
		[]string{"level", "cgroup_id"},
	)
	psiIO = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "psi_io_stall_seconds_total", Help: "Per-cgroup IO pressure stall time (seconds)."},
		[]string{"level", "cgroup_id"},
	)
	psiMemory = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "psi_memory_stall_seconds_total", Help: "Per-cgroup memory pressure stall time (seconds)."},
		[]string{"level", "cgroup_id"},
	)
)

const (
	memBytesAllocType = 1
	memBytesFreeType  = 2
)

func labelPF(t uint32) string {
	if t == 1 {
		return "user"
	}
	if t == 2 {
		return "kernel"
	}
	return "unknown"
}
func labelSC(t uint32) string {
	if t == 1 {
		return "switch"
	}
	if t == 2 {
		return "wakeup"
	}
	return "unknown"
}
func labelCPU(t uint32) string {
	switch t {
	case 1:
		return "runtime"
	case 2:
		return "wait"
	case 3:
		return "iowait"
	default:
		return "unknown"
	}
}
func labelMemEvent(t uint32) string {
	if t == 1 {
		return "direct_reclaim"
	}
	return "unknown"
}
func labelMemPages(t uint32) string {
	if t == 1 {
		return "reclaim_pages"
	}
	return "unknown"
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func getenvUint(key string, def uint) uint {
	if v := os.Getenv(key); v != "" {
		var x uint
		if _, err := fmt.Sscanf(v, "%d", &x); err == nil {
			return x
		}
	}
	return def
}

func isUnifiedCgroupV2(root string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		return false
	}
	const CGROUP2_SUPER_MAGIC = 0x63677270
	return st.Type == CGROUP2_SUPER_MAGIC
}
func cgroupInode(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st := fi.Sys().(*syscall.Stat_t)
	return uint64(st.Ino), nil
}
func cgroupLevel(root, path string) (uint32, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return 0, err
	}
	if rel == "." {
		return 0, nil
	}
	segs := strings.Split(rel, string(filepath.Separator))
	n := 0
	for _, s := range segs {
		if s != "" {
			n++
		}
	}
	return uint32(n), nil
}

func collectAllowed(root string) (map[uint64]string, error) {
	allowed := make(map[uint64]string)
	add := func(path string) error {
		ino, err := cgroupInode(path)
		if err != nil {
			return err
		}
		allowed[ino] = path
		return nil
	}
	if err := add(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return allowed, err
	}
	var agg error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := add(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
			agg = errors.Join(agg, err)
		}
	}
	return allowed, agg
}

// --- modules typed holders (含 config map) ---
type pfModule struct {
	ProgU *ebpf.Program `ebpf:"tp_page_fault_user"`
	ProgK *ebpf.Program `ebpf:"tp_page_fault_kernel"`
	MapPF *ebpf.Map     `ebpf:"cg_pf_cnt"`
	Cfg   *ebpf.Map     `ebpf:"cfg_map"` // ← 原本是 "config"
}
type schedModule struct {
	Sw   *ebpf.Program `ebpf:"tp_sched_switch"`
	Wkp  *ebpf.Program `ebpf:"tp_sched_wakeup"`
	Exit *ebpf.Program `ebpf:"tp_sched_exit"`
	MapS *ebpf.Map     `ebpf:"cg_sched_cnt"`
	Cfg  *ebpf.Map     `ebpf:"cfg_map"` // ←
}
type sysModule struct {
	En   *ebpf.Program `ebpf:"tp_sys_enter"`
	Ex   *ebpf.Program `ebpf:"tp_sys_exit"`
	MapC *ebpf.Map     `ebpf:"cg_sys_cnt"`
	MapL *ebpf.Map     `ebpf:"cg_sys_lat_ns"`
	Cfg  *ebpf.Map     `ebpf:"cfg_map"` // ←
}
type cpuModule struct {
	Run    *ebpf.Program `ebpf:"tp_sched_stat_runtime"`
	Wait   *ebpf.Program `ebpf:"tp_sched_stat_wait"`
	IO     *ebpf.Program `ebpf:"tp_sched_stat_iowait"`
	Switch *ebpf.Program `ebpf:"tp_sched_switch_cpu"`
	Exit   *ebpf.Program `ebpf:"tp_sched_exit_cpu"`
	MapNs  *ebpf.Map     `ebpf:"cg_cpu_ns"`
	Cfg    *ebpf.Map     `ebpf:"cfg_map"`
}
type memModule struct {
	Kswapd    *ebpf.Program `ebpf:"tp_mm_vmscan_kswapd_wake"`
	Direct    *ebpf.Program `ebpf:"tp_mm_vmscan_direct_reclaim_begin"`
	Reclaim   *ebpf.Program `ebpf:"tp_mm_vmscan_reclaim_pages"`
	Alloc     *ebpf.Program `ebpf:"tp_mm_page_alloc"`
	Free      *ebpf.Program `ebpf:"tp_mm_page_free"`
	MapEvt    *ebpf.Map     `ebpf:"cg_mem_evt"`
	MapPages  *ebpf.Map     `ebpf:"cg_mem_pages"`
	MapBytes  *ebpf.Map     `ebpf:"cg_mem_bytes"`
	MapKswapd *ebpf.Map     `ebpf:"kswapd_cnt"`
	Cfg       *ebpf.Map     `ebpf:"cfg_map"`
}

func loadModule(path string, rew map[string]interface{}, out interface{}) (func(), error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("load spec %s: %w", path, err)
	}
	// 保留 RewriteConstants 作為 fallback；主要控制用 config map 熱更新
	if len(rew) > 0 {
		if err := spec.RewriteConstants(rew); err != nil {
			return nil, fmt.Errorf("rewrite consts %s: %w", path, err)
		}
	}
	if err := spec.LoadAndAssign(out, nil); err != nil {
		return nil, fmt.Errorf("load&assign %s: %w", path, err)
	}
	return func() {
		switch m := out.(type) {
		case *pfModule:
			if m.ProgU != nil {
				m.ProgU.Close()
			}
			if m.ProgK != nil {
				m.ProgK.Close()
			}
			if m.MapPF != nil {
				m.MapPF.Close()
			}
			if m.Cfg != nil {
				m.Cfg.Close()
			}
		case *schedModule:
			if m.Sw != nil {
				m.Sw.Close()
			}
			if m.Wkp != nil {
				m.Wkp.Close()
			}
			if m.Exit != nil {
				m.Exit.Close()
			}
			if m.MapS != nil {
				m.MapS.Close()
			}
			if m.Cfg != nil {
				m.Cfg.Close()
			}
		case *sysModule:
			if m.En != nil {
				m.En.Close()
			}
			if m.Ex != nil {
				m.Ex.Close()
			}
			if m.MapC != nil {
				m.MapC.Close()
			}
			if m.MapL != nil {
				m.MapL.Close()
			}
			if m.Cfg != nil {
				m.Cfg.Close()
			}
		case *cpuModule:
			if m.Run != nil {
				m.Run.Close()
			}
			if m.Wait != nil {
				m.Wait.Close()
			}
			if m.IO != nil {
				m.IO.Close()
			}
			if m.Switch != nil {
				m.Switch.Close()
			}
			if m.Exit != nil {
				m.Exit.Close()
			}
			if m.MapNs != nil {
				m.MapNs.Close()
			}
			if m.Cfg != nil {
				m.Cfg.Close()
			}
		case *memModule:
			if m.Kswapd != nil {
				m.Kswapd.Close()
			}
			if m.Direct != nil {
				m.Direct.Close()
			}
			if m.Reclaim != nil {
				m.Reclaim.Close()
			}
			if m.Alloc != nil {
				m.Alloc.Close()
			}
			if m.Free != nil {
				m.Free.Close()
			}
			if m.MapEvt != nil {
				m.MapEvt.Close()
			}
			if m.MapPages != nil {
				m.MapPages.Close()
			}
			if m.MapBytes != nil {
				m.MapBytes.Close()
			}
			if m.MapKswapd != nil {
				m.MapKswapd.Close()
			}
			if m.Cfg != nil {
				m.Cfg.Close()
			}
		}
	}, nil
}

func attachTracepoint(cat, name string, prog *ebpf.Program) link.Link {
	if prog == nil {
		return nil
	}
	lnk, err := link.Tracepoint(cat, name, prog, nil)
	if err != nil {
		log.Printf("attach %s/%s failed: %v", cat, name, err)
		return nil
	}
	return lnk
}

func writeCfgAll(c cfg, mods ...interface{}) {
	key := uint32(0)
	for _, m := range mods {
		switch mo := m.(type) {
		case *pfModule:
			if mo != nil && mo.Cfg != nil {
				_ = mo.Cfg.Update(&key, &c, ebpf.UpdateAny)
			}
		case *schedModule:
			if mo != nil && mo.Cfg != nil {
				_ = mo.Cfg.Update(&key, &c, ebpf.UpdateAny)
			}
		case *sysModule:
			if mo != nil && mo.Cfg != nil {
				_ = mo.Cfg.Update(&key, &c, ebpf.UpdateAny)
			}
		case *cpuModule:
			if mo != nil && mo.Cfg != nil {
				_ = mo.Cfg.Update(&key, &c, ebpf.UpdateAny)
			}
		case *memModule:
			if mo != nil && mo.Cfg != nil {
				_ = mo.Cfg.Update(&key, &c, ebpf.UpdateAny)
			}
		}
	}
}

func main() {
	prometheus.MustRegister(pageFaults, schedEvents, sysCalls, sysLatency, cpuSched, memEvents, memPages, pageAllocBytes, pageFreeBytes, kswapdWakeups, memoryFaults, psiCPU, psiIO, psiMemory)

	cgroupRoot := getenv("CGROUP_ROOT", cgroupRootDefault)
	targetPath := getenv("PF_TARGET_CGROUP", gockerPathDefault)
	sampleRate := getenvUint("PF_SAMPLE_RATE", 1)

	if !isUnifiedCgroupV2(cgroupRoot) {
		log.Fatalf("require cgroup v2 at %s", cgroupRoot)
	}
	tgid, err := cgroupInode(targetPath)
	if err != nil {
		log.Fatalf("stat %s: %v", targetPath, err)
	}
	level, err := cgroupLevel(cgroupRoot, targetPath)
	if err != nil {
		log.Fatalf("compute level: %v", err)
	}

	cfg0 := cfg{SampleRate: uint32(sampleRate), EnableFilter: 1, TargetLevel: uint32(level), TargetCgid: uint64(tgid)}
	log.Printf("Target subtree %s (inode=%d, level=%d), sample_rate=%d", targetPath, tgid, level, sampleRate)

	var (
		allowedMu    sync.RWMutex
		allowed      map[uint64]string
		allowedCount int
	)
	// 說明：memoryLast / psiLast* 以 cgroup id 保存前次的 memory.stat、PSI total 數值
	memoryLast := make(map[uint64]memCounters)
	psiLastCPU := make(map[uint64]psiCounters)
	psiLastIO := make(map[uint64]psiCounters)
	psiLastMem := make(map[uint64]psiCounters)

	// 說明：pruneState() 在白名單更新後移除不存在的 cgroup 狀態，避免資料殘留
	pruneState := func(set map[uint64]string) {
		for cgid := range memoryLast {
			if _, ok := set[cgid]; !ok {
				delete(memoryLast, cgid)
			}
		}
		for cgid := range psiLastCPU {
			if _, ok := set[cgid]; !ok {
				delete(psiLastCPU, cgid)
			}
		}
		for cgid := range psiLastIO {
			if _, ok := set[cgid]; !ok {
				delete(psiLastIO, cgid)
			}
		}
		for cgid := range psiLastMem {
			if _, ok := set[cgid]; !ok {
				delete(psiLastMem, cgid)
			}
		}
	}
	refreshAllowed := func() {
		set, err := collectAllowed(targetPath)
		if err != nil {
			log.Printf("collect allowed from %s: %v", targetPath, err)
		}
		if set == nil {
			set = make(map[uint64]string)
		}
		if len(set) == 0 {
			set[cfg0.TargetCgid] = targetPath
		}
		allowedMu.Lock()
		allowed = set
		allowedMu.Unlock()
		pruneState(set)
		if len(set) != allowedCount {
			allowedCount = len(set)
			log.Printf("allowed cgroup whitelist size=%d", allowedCount)
		}
	}
	refreshAllowed()

	// 載入各模組（無 pin，程序退出 maps 就銷毀）
	var pf pfModule
	var sched schedModule
	var sys sysModule
	var cpu cpuModule
	var mem memModule

	rew := map[string]interface{}{"SAMPLE_RATE": uint32(sampleRate), "ENABLE_FILTER": uint32(1), "TARGET_LEVEL": uint32(level), "TARGET_CGID": uint64(tgid)}
	if closer, err := loadModule("bpf/pf.bpf.o", rew, &pf); err != nil {
		log.Printf("pf module skipped: %v", err)
	} else {
		defer closer()
	}
	if closer, err := loadModule("bpf/sched.bpf.o", rew, &sched); err != nil {
		log.Printf("sched module skipped: %v", err)
	} else {
		defer closer()
	}
	if closer, err := loadModule("bpf/sys.bpf.o", rew, &sys); err != nil {
		log.Printf("sys module skipped: %v", err)
	} else {
		defer closer()
	}
	if closer, err := loadModule("bpf/cpu.bpf.o", rew, &cpu); err != nil {
		log.Printf("cpu module skipped: %v", err)
	} else {
		defer closer()
	}
	if closer, err := loadModule("bpf/mem.bpf.o", rew, &mem); err != nil {
		log.Printf("mem module skipped: %v", err)
	} else {
		defer closer()
	}

	writeCfgAll(cfg0, &pf, &sched, &sys, &cpu, &mem)

	links := []link.Link{
		attachTracepoint("exceptions", "page_fault_user", pf.ProgU),
		attachTracepoint("exceptions", "page_fault_kernel", pf.ProgK),
		attachTracepoint("sched", "sched_switch", sched.Sw),
		attachTracepoint("sched", "sched_wakeup", sched.Wkp),
		attachTracepoint("sched", "sched_process_exit", sched.Exit),
		attachTracepoint("raw_syscalls", "sys_enter", sys.En),
		attachTracepoint("raw_syscalls", "sys_exit", sys.Ex),
		attachTracepoint("sched", "sched_stat_runtime", cpu.Run),
		attachTracepoint("sched", "sched_stat_wait", cpu.Wait),
		attachTracepoint("sched", "sched_stat_iowait", cpu.IO),
		attachTracepoint("sched", "sched_switch", cpu.Switch),
		attachTracepoint("sched", "sched_process_exit", cpu.Exit),
		attachTracepoint("vmscan", "mm_vmscan_kswapd_wake", mem.Kswapd),
		attachTracepoint("vmscan", "mm_vmscan_direct_reclaim_begin", mem.Direct),
		attachTracepoint("vmscan", "mm_vmscan_reclaim_pages", mem.Reclaim),
		attachTracepoint("kmem", "mm_page_alloc", mem.Alloc),
		attachTracepoint("kmem", "mm_page_free", mem.Free),
	}
	defer func() {
		for _, l := range links {
			if l != nil {
				l.Close()
			}
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/admin/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in cfg
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if in.SampleRate == 0 {
			in.SampleRate = cfg0.SampleRate
		}
		if in.TargetLevel == 0 {
			in.TargetLevel = cfg0.TargetLevel
		}
		if in.TargetCgid == 0 {
			in.TargetCgid = cfg0.TargetCgid
		}
		if in.EnableFilter != 0 && in.EnableFilter != 1 {
			in.EnableFilter = 1
		}
		cfg0 = in
		writeCfgAll(cfg0, &pf, &sched, &sys, &cpu, &mem)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		log.Printf("config updated: %+v", cfg0)
	})
	go func() {
		log.Printf("HTTP: metrics on :2112/metrics ; admin on :2112/admin/config (POST JSON)")
		log.Fatal(http.ListenAndServe(":2112", nil))
	}()

	ncpu := runtime.NumCPU()
	val := make([]uint64, ncpu)
	type lastMap = map[cgKey]uint64
	lastPF := make(lastMap, 4096)
	lastSC := make(lastMap, 4096)
	lastSysCnt := make(lastMap, 16384)
	lastSysSum := make(lastMap, 16384)
	lastCPU := make(lastMap, 4096)
	lastMemEvt := make(lastMap, 1024)
	lastMemPages := make(lastMap, 1024)
	lastMemBytes := make(lastMap, 1024)
	var lastKswapd uint64

	allowedPath := func(cgid uint64) (string, bool) {
		allowedMu.RLock()
		defer allowedMu.RUnlock()
		if allowed == nil {
			return "", false
		}
		path, ok := allowed[cgid]
		return path, ok
	}
	snapshotAllowed := func() map[uint64]string {
		allowedMu.RLock()
		defer allowedMu.RUnlock()
		if allowed == nil {
			return nil
		}
		out := make(map[uint64]string, len(allowed))
		for cgid, path := range allowed {
			out[cgid] = path
		}
		return out
	}

	// 說明：scan() 遍歷 eBPF maps，計算差分後餵給 Prometheus Counter
	scan := func(m *ebpf.Map, last lastMap, apply func(k cgKey, delta uint64)) {
		if m == nil {
			return
		}
		var k cgKey
		it := m.Iterate()
		for it.Next(&k, &val) {
			var total uint64
			for _, v := range val {
				total += v
			}
			if _, ok := allowedPath(k.Cgid); !ok {
				delete(last, k)
				continue
			}
			prev := last[k]
			if total >= prev {
				d := total - prev
				if d > 0 {
					apply(k, d)
				}
			}
			last[k] = total
		}
		if err := it.Err(); err != nil {
			log.Printf("iterate map: %v", err)
		}
	}

	scanArray := func(m *ebpf.Map, last *uint64, apply func(delta uint64)) {
		if m == nil {
			return
		}
		var key uint32
		if err := m.Lookup(&key, &val); err != nil {
			if err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				log.Printf("lookup array map: %v", err)
			}
			return
		}
		var total uint64
		for _, v := range val {
			total += v
		}
		if total >= *last {
			if d := total - *last; d > 0 {
				apply(d)
			}
		}
		*last = total
	}

	readMemoryStat := func(dir string) (uint64, uint64, error) {
		f, err := os.Open(filepath.Join(dir, "memory.stat"))
		if err != nil {
			return 0, 0, err
		}
		defer f.Close()

		var pgfault, pgmaj uint64
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) != 2 {
				continue
			}
			val, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] {
			case "pgfault":
				pgfault = val
			case "pgmajfault":
				pgmaj = val
			}
		}
		if err := sc.Err(); err != nil {
			return 0, 0, err
		}
		return pgfault, pgmaj, nil
	}

	readPSI := func(file string) (uint64, uint64, error) {
		f, err := os.Open(file)
		if err != nil {
			return 0, 0, err
		}
		defer f.Close()

		var some, full uint64
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) == 0 {
				continue
			}
			level := fields[0]
			for _, token := range fields[1:] {
				if !strings.HasPrefix(token, "total=") {
					continue
				}
				val, err := strconv.ParseUint(strings.TrimPrefix(token, "total="), 10, 64)
				if err != nil {
					continue
				}
				if level == "some" {
					some = val
				} else if level == "full" {
					full = val
				}
			}
		}
		if err := sc.Err(); err != nil {
			return 0, 0, err
		}
		return some, full, nil
	}

	updateMemory := func() {
		paths := snapshotAllowed()
		if len(paths) == 0 {
			return
		}
		for cgid, dir := range paths {
			if cgid == cfg0.TargetCgid {
				continue
			}
			pgfault, pgmaj, err := readMemoryStat(dir)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Printf("read memory.stat for %s: %v", dir, err)
				}
				continue
			}
			var pgminor uint64
			if pgfault >= pgmaj {
				pgminor = pgfault - pgmaj
			}
			// 說明：major/minor fault 透過 memoryLast 記錄上次數值，做差分後累加
			if prev, ok := memoryLast[cgid]; ok {
				id := strconv.FormatUint(cgid, 10)
				if pgmaj >= prev.Major {
					d := pgmaj - prev.Major
					if d > 0 {
						memoryFaults.WithLabelValues("major", id).Add(float64(d))
					}
				}
				if pgminor >= prev.Minor {
					d := pgminor - prev.Minor
					if d > 0 {
						memoryFaults.WithLabelValues("minor", id).Add(float64(d))
					}
				}
			}
			memoryLast[cgid] = memCounters{Major: pgmaj, Minor: pgminor}
		}
	}

	updatePSI := func() {
		paths := snapshotAllowed()
		if len(paths) == 0 {
			return
		}
		for cgid, dir := range paths {
			if cgid == cfg0.TargetCgid {
				continue
			}
			id := strconv.FormatUint(cgid, 10)

			if some, full, err := readPSI(filepath.Join(dir, "cpu.pressure")); err == nil {
				// 說明：PSI total 單位為微秒，因此差分後需除以 1e6 轉換成秒
				if prev, ok := psiLastCPU[cgid]; ok {
					if some >= prev.Some {
						if d := some - prev.Some; d > 0 {
							psiCPU.WithLabelValues("some", id).Add(float64(d) / 1e6)
						}
					}
					if full >= prev.Full {
						if d := full - prev.Full; d > 0 {
							psiCPU.WithLabelValues("full", id).Add(float64(d) / 1e6)
						}
					}
				}
				psiLastCPU[cgid] = psiCounters{Some: some, Full: full}
			} else if !errors.Is(err, os.ErrNotExist) {
				log.Printf("read cpu.pressure for %s: %v", dir, err)
			}

			if some, full, err := readPSI(filepath.Join(dir, "io.pressure")); err == nil {
				if prev, ok := psiLastIO[cgid]; ok {
					if some >= prev.Some {
						if d := some - prev.Some; d > 0 {
							psiIO.WithLabelValues("some", id).Add(float64(d) / 1e6)
						}
					}
					if full >= prev.Full {
						if d := full - prev.Full; d > 0 {
							psiIO.WithLabelValues("full", id).Add(float64(d) / 1e6)
						}
					}
				}
				psiLastIO[cgid] = psiCounters{Some: some, Full: full}
			} else if !errors.Is(err, os.ErrNotExist) {
				log.Printf("read io.pressure for %s: %v", dir, err)
			}

			if some, full, err := readPSI(filepath.Join(dir, "memory.pressure")); err == nil {
				if prev, ok := psiLastMem[cgid]; ok {
					if some >= prev.Some {
						if d := some - prev.Some; d > 0 {
							psiMemory.WithLabelValues("some", id).Add(float64(d) / 1e6)
						}
					}
					if full >= prev.Full {
						if d := full - prev.Full; d > 0 {
							psiMemory.WithLabelValues("full", id).Add(float64(d) / 1e6)
						}
					}
				}
				psiLastMem[cgid] = psiCounters{Some: some, Full: full}
			} else if !errors.Is(err, os.ErrNotExist) {
				log.Printf("read memory.pressure for %s: %v", dir, err)
			}
		}
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	log.Printf("Scanning maps each 1s…")
	for range ticker.C {
		refreshAllowed()
		scan(pf.MapPF, lastPF, func(k cgKey, d uint64) {
			pageFaults.WithLabelValues(labelPF(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(sched.MapS, lastSC, func(k cgKey, d uint64) {
			schedEvents.WithLabelValues(labelSC(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(sys.MapC, lastSysCnt, func(k cgKey, d uint64) {
			sysCalls.WithLabelValues(labelSys(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(sys.MapL, lastSysSum, func(k cgKey, d uint64) {
			sysLatency.WithLabelValues(labelSys(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(cpu.MapNs, lastCPU, func(k cgKey, d uint64) {
			cpuSched.WithLabelValues(labelCPU(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(mem.MapEvt, lastMemEvt, func(k cgKey, d uint64) {
			memEvents.WithLabelValues(labelMemEvent(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(mem.MapPages, lastMemPages, func(k cgKey, d uint64) {
			memPages.WithLabelValues(labelMemPages(k.Type), fmt.Sprintf("%d", k.Cgid)).Add(float64(d))
		})
		scan(mem.MapBytes, lastMemBytes, func(k cgKey, d uint64) {
			id := fmt.Sprintf("%d", k.Cgid)
			switch k.Type {
			case memBytesAllocType:
				pageAllocBytes.WithLabelValues(id).Add(float64(d))
			case memBytesFreeType:
				pageFreeBytes.WithLabelValues(id).Add(float64(d))
			}
		})
		scanArray(mem.MapKswapd, &lastKswapd, func(d uint64) {
			kswapdWakeups.Add(float64(d))
		})
		updateMemory()
		updatePSI()
	}
}
