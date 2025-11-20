#!/usr/bin/env bash
set -euo pipefail

# === 先決條件檢查 ===
if ! mount | grep -q "on /sys/fs/cgroup type cgroup2"; then
  echo "[ERR] cgroup v2 未掛載在 /sys/fs/cgroup"; exit 1
fi
test -e /sys/kernel/btf/vmlinux || { echo "[ERR] 缺少 BTF：/sys/kernel/btf/vmlinux"; exit 1; }
command -v bpftool >/dev/null || { echo "[ERR] 需要 bpftool"; exit 1; }
command -v clang >/dev/null || { echo "[ERR] 需要 clang"; exit 1; }

# === 編譯 eBPF ===
echo "[*] Build eBPF objects..."
make -C bpf clean && make -C bpf

# === 編譯 exporter / ctl ===
echo "[*] Build exporter..."
pushd cmd/collector >/dev/null
go mod tidy
go build -o ../../collector .
popd >/dev/null

echo "[*] Build ctl (optional)..."
pushd cmd/ctl >/dev/null
go mod tidy || true
go build -o ../../ctl || true
popd >/dev/null

# === 啟動 exporter ===
echo "[*] Start exporter (sudo)..."
sudo PF_TARGET_CGROUP=/sys/fs/cgroup/gocker PF_SAMPLE_RATE=1 ./collector &
