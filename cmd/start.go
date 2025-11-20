// cmd/start.go
package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gocker/internal/config"
	"gocker/internal/tty"
	"gocker/internal/types"
)

var (
	startAttach      bool
	startAllocateTTY bool
)

var startCommand = &cobra.Command{
	Use:   "start CONTAINER",
	Short: "Restart a stopped container",
	Long:  "Restart a stopped container and optionally attach to its console.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		containerIdentifier := args[0]

		conn, err := net.Dial("unix", config.SocketPath)
		if err != nil {
			logrus.Fatalf("cannot connect to gocker-daemon: %v", err)
		}
		defer conn.Close()

		attach := startAttach
		allocateTTY := startAllocateTTY
		stdinFD := int(os.Stdin.Fd())
		if attach && allocateTTY && !term.IsTerminal(stdinFD) {
			logrus.Warn("this terminal does not support TTY, automatically downgrading to non-TTY mode")
			allocateTTY = false
		}

		startReq := types.StartRequest{
			ContainerID: containerIdentifier,
			Attach:      attach,
			Tty:         allocateTTY,
		}

		payload, err := json.Marshal(startReq)
		if err != nil {
			logrus.Fatalf("序列化 start 請求失敗: %v", err)
		}

		req := types.Request{
			Command: "start",
			Payload: payload,
		}

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			logrus.Fatalf("發送 start 請求失敗: %v", err)
		}

		if !attach {
			var res types.Response
			if err := json.NewDecoder(conn).Decode(&res); err != nil {
				logrus.Fatalf("cannot read start response: %v", err)
			}
			if res.Status == "success" {
				logrus.Info(res.Message)
			} else {
				logrus.Fatalf("error from daemon: %s", res.Message)
			}
			return
		}

		if allocateTTY && term.IsTerminal(stdinFD) {
			oldState, err := term.MakeRaw(stdinFD)
			if err != nil {
				logrus.Fatalf("something went wrong while setting terminal to raw mode: %v", err)
			}
			defer term.Restore(stdinFD, oldState)
		}

		var once sync.Once
		done := make(chan struct{})
		closeDone := func() {
			once.Do(func() {
				close(done)
			})
		}

		go func() {
			if _, err := io.Copy(os.Stdout, conn); err != nil && !errors.Is(err, io.EOF) {
				logrus.WithError(err).Warn("something went wrong while reading container output")
			}
			closeDone()
		}()

		stdinDone := make(chan struct{})
		go func() {
			defer close(stdinDone)
			tty.CopyInputUntilClosed(conn, os.Stdin, done)
		}()

		<-done

		if unixConn, ok := conn.(*net.UnixConn); ok {
			_ = unixConn.CloseRead()
			_ = unixConn.CloseWrite()
		}

		<-stdinDone
		logrus.Info("container has exited")
	},
}

func init() {
	startAttach = true
	startAllocateTTY = true
	rootCmd.AddCommand(startCommand)
	startCommand.Flags().BoolVarP(&startAttach, "attach", "a", true, "attach container's STDIN/STDOUT/STDERR")
	startCommand.Flags().BoolVarP(&startAllocateTTY, "tty", "t", true, "allocate a pseudo-TTY for the start command")
}
