// cmd/gocker/main.go

package main

// execute `make` in eBPF directory if `ebpf-sched-monitor` binary not found in `eBPF` directory
//go:generate bash -c "[ -f ./eBPF/ebpf-sched-monitor ] || (cd eBPF && make)"
import (
	"os"

	"gocker/cmd"
	"gocker/internal/container"

	"github.com/sirupsen/logrus"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		logrus.Info("Executing internal 'init' command for container setup")
		if err := container.InitContainer(); err != nil {
			logrus.Fatalf("容器初始化失敗: %v", err)
		}
		return
	}
	cmd.Execute()
}
