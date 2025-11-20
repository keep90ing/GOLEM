# bpf/Makefile
ARCH := $(shell uname -m)
ifeq ($(ARCH),x86_64)
  TARGET_ARCH := x86
else ifeq ($(ARCH),aarch64)
  TARGET_ARCH := arm64
else
  $(error Unsupported arch $(ARCH); set TARGET_ARCH manually)
endif

CLANG ?= clang
BPF_CFLAGS := -O2 -g -target bpf -D__TARGET_ARCH_$(TARGET_ARCH) \
              -Wall -Wno-unused-value -Wno-pointer-sign -Wno-compare-distinct-pointer-types

all: vmlinux.h pf.bpf.o sched.bpf.o sys.bpf.o cpu.bpf.o mem.bpf.o

vmlinux.h:
	@if [ ! -e /sys/kernel/btf/vmlinux ]; then \
	  echo "ERROR: /sys/kernel/btf/vmlinux not found"; exit 1; \
	fi
	@which bpftool >/dev/null 2>&1 || (echo "ERROR: bpftool not found"; exit 1)
	bpftool btf dump file /sys/kernel/btf/vmlinux format c > vmlinux.h

pf.bpf.o: pf.bpf.c vmlinux.h
	$(CLANG) $(BPF_CFLAGS) -c $< -o $@

sched.bpf.o: sched.bpf.c vmlinux.h
	$(CLANG) $(BPF_CFLAGS) -c $< -o $@

sys.bpf.o: sys.bpf.c vmlinux.h
	$(CLANG) $(BPF_CFLAGS) -c $< -o $@

cpu.bpf.o: cpu.bpf.c vmlinux.h
	$(CLANG) $(BPF_CFLAGS) -c $< -o $@

mem.bpf.o: mem.bpf.c vmlinux.h
	$(CLANG) $(BPF_CFLAGS) -c $< -o $@

clean:
	rm -f *.bpf.o vmlinux.h
