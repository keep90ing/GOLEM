// cmd/ps.go
package cmd

import (
	"encoding/json"
	"fmt"
	"gocker/internal/api"
	"gocker/internal/types"
	"os"
	"text/tabwriter"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var psCommand = &cobra.Command{
	Use:   "ps",
	Short: "List all containers",
	Run: func(cmd *cobra.Command, args []string) {
		req := types.Request{Command: "ps"}

		res, err := api.SendRequest(req)
		if err != nil {
			logrus.Fatalf("與 gocker-daemon 通訊失敗: %v", err)
		}
		if res.Status != "success" {
			logrus.Fatalf("來自 Daemon 的錯誤: %s", res.Message)
		}

		var containers []types.ContainerInfo
		if err := json.Unmarshal(res.Data, &containers); err != nil {
			logrus.Fatalf("解析來自 Daemon 的數據失敗: %v", err)
		}
		w := tabwriter.NewWriter(os.Stdout, 12, 1, 3, ' ', 0)
		fmt.Fprint(w, "ID\tNAME\tIMAGE\tCOMMAND\tSTATUS\n")
		for _, c := range containers {
			if len(c.ID) < 12 {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				c.ID[:12],
				c.Name,
				c.Image,
				c.Command,
				c.Status)
		}
		if err := w.Flush(); err != nil {
			logrus.Errorf("Failed to flush output: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(psCommand)
}
