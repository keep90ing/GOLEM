// cmd/images.go
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"gocker/internal/api"
	"gocker/internal/types"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var imagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List all locally stored images",
	Long:  `Lists all images that have been pulled and are stored locally in the gocker storage path.`,
	Run: func(cmd *cobra.Command, args []string) {
		req := types.Request{Command: "images"}

		res, err := api.SendRequest(req)
		if err != nil {
			logrus.Fatalf("與 gocker-daemon 通訊失敗: %v", err)
		}

		if res.Status != "success" {
			logrus.Fatalf("來自 Daemon 的錯誤: %s", res.Message)
		}

		var imageList []string
		if err := json.Unmarshal(res.Data, &imageList); err != nil {
			logrus.Fatalf("解析來自 Daemon 的映像檔列表失敗: %v", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 12, 1, 3, ' ', 0)
		fmt.Fprint(w, "REPOSITORY\tTAG\n")
		for _, imgName := range imageList {
			parts := strings.Split(imgName, ":")
			repo := parts[0]
			tag := "latest"
			if len(parts) > 1 {
				tag = parts[1]
			}
			fmt.Fprintf(w, "%s\t%s\n", repo, tag)
		}

		if err := w.Flush(); err != nil {
			logrus.Errorf("Failed to flush output: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(imagesCmd)
}
