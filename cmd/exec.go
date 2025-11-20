package cmd

import (
	"fmt"
	"gocker/internal/types"
	"io"
	"net"
	"os"

	"encoding/json"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var allocateTty bool

var execCommand = &cobra.Command{
	Use:   "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
	Short: "Execute a command in a running container",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		containerIDOrName := args[0]
		command := args[1:]

		execReq := types.ExecRequest{
			ContainerID: containerIDOrName,
			Command:     command,
			Tty:         allocateTty,
		}
		payload, err := json.Marshal(execReq)
		if err != nil {
			logrus.Fatalf("序列化 exec 請求失敗: %v", err)
		}

		req := types.Request{
			Command: "exec",
			Payload: payload,
		}

		conn, err := net.Dial("unix", "/var/run/gocker.sock")
		if err != nil {
			logrus.Fatalf("無法連接到 gocker-daemon: %v", err)
		}

		if allocateTty && term.IsTerminal(int(os.Stdin.Fd())) {
			oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
			if err != nil {
				logrus.Fatalf("設定終端機為 Raw Mode 失敗: %v", err)
			}
			defer term.Restore(int(os.Stdin.Fd()), oldState)
		}

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			logrus.Fatalf("發送 exec 請求失敗: %v", err)
		}

		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(os.Stdout, conn)
			done <- struct{}{}
		}()

		_, _ = io.Copy(conn, os.Stdin)
		<-done
		fmt.Println("\nExec session finished.")
	},
}

func init() {
	rootCmd.AddCommand(execCommand)
	execCommand.Flags().BoolVarP(&allocateTty, "tty", "t", false, "分配一個虛擬終端 (pseudo-TTY)")
}
